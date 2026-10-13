package cli

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/langtind/ctlkit"

	"github.com/langtind/skolectl/internal/flyt"
)

const (
	kindDirect = "direct"
	kindGroup  = "group"
)

// threadRef names one thread at the service. Its text form is the thread id
// that threads prints: "<pupil>.d.<employee>" for a direct thread with one
// employee, "<pupil>.g.<conversation>" for a group conversation.
type threadRef struct {
	Pupil        int
	Employee     int
	Conversation string
}

func (r threadRef) String() string {
	if r.Conversation != "" {
		return fmt.Sprintf("%d.g.%s", r.Pupil, r.Conversation)
	}
	return fmt.Sprintf("%d.d.%d", r.Pupil, r.Employee)
}

func (r threadRef) kind() string {
	if r.Conversation != "" {
		return kindGroup
	}
	return kindDirect
}

func parseThreadRef(id string) (threadRef, error) {
	bad := func() (threadRef, error) {
		return threadRef{}, ctlkit.Usagef("%q is not a thread id", id).WithHint(`run "%s threads" to see the ids.`, Name)
	}
	parts := strings.SplitN(id, ".", 3)
	if len(parts) != 3 || parts[2] == "" {
		return bad()
	}
	pupil, err := strconv.Atoi(parts[0])
	if err != nil {
		return bad()
	}
	switch parts[1] {
	case "d":
		employee, err := strconv.Atoi(parts[2])
		if err != nil {
			return bad()
		}
		return threadRef{Pupil: pupil, Employee: employee}, nil
	case "g":
		return threadRef{Pupil: pupil, Conversation: parts[2]}, nil
	}
	return bad()
}

// child is one child as children prints it.
type child struct {
	PupilID int    `json:"pupilId"`
	Name    string `json:"name"`
	School  string `json:"school"`
	Level   int    `json:"level"`
	// SchoolID names the school in requests. It is not printed.
	SchoolID string `json:"-"`
}

type childList struct {
	Items []child `json:"items"`
}

// schoolChildren lists the children that go to a school.
func (a *App) schoolChildren(ctx context.Context) ([]child, error) {
	all, err := a.Client.Children(ctx)
	if err != nil {
		return nil, err
	}
	children := []child{}
	for _, c := range all {
		if c.School == nil {
			continue
		}
		children = append(children, child{
			PupilID:  c.School.PupilID,
			Name:     strings.TrimSpace(c.FirstName + " " + c.LastName),
			School:   c.School.OrganizationName,
			Level:    c.School.Level,
			SchoolID: c.School.SchoolID,
		})
	}
	return children, nil
}

func (a *App) children(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.schoolChildren(ctx)
	if err != nil {
		return err
	}
	list := childList{Items: children}
	return c.Print(list, func(w io.Writer) { renderChildren(w, list) })
}

// thread is one thread as threads prints it.
type thread struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	PupilID int    `json:"pupilId"`
	Child   string `json:"child"`
	Title   string `json:"title"`
	// Employee is who a direct thread is with.
	Employee string `json:"employee,omitempty"`
	// Preview is the start of the latest message.
	Preview       string `json:"preview"`
	LastMessageAt string `json:"lastMessageAt"`
	Unread        int    `json:"unread"`
	ReadOnly      bool   `json:"readOnly"`
}

type threadList struct {
	Items     []thread `json:"items"`
	Truncated bool     `json:"truncated"`
}

func (a *App) threads(ctx context.Context, c *ctlkit.Call) error {
	limit := c.Int("limit")
	if limit < 1 || limit > maxLimit {
		return ctlkit.Usagef("--limit takes 1 to %d, not %d", maxLimit, limit)
	}
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}

	kind := c.String("kind")
	threads := []thread{}
	for _, kid := range children {
		if kind != kindGroup {
			direct, err := a.Client.DirectThreads(ctx, kid.PupilID)
			if err != nil {
				return err
			}
			for _, d := range direct {
				threads = append(threads, thread{
					ID:            threadRef{Pupil: kid.PupilID, Employee: d.EmployeeID}.String(),
					Kind:          kindDirect,
					PupilID:       kid.PupilID,
					Child:         kid.Name,
					Title:         d.Title,
					Employee:      d.Sender,
					Preview:       d.MessageText,
					LastMessageAt: stamp(d.SentDate.Time),
					Unread:        d.UnreadCount,
				})
			}
		}
		if kind != kindDirect {
			group, err := a.Client.GroupThreads(ctx, kid.PupilID)
			if err != nil {
				return err
			}
			for _, g := range group {
				threads = append(threads, thread{
					ID:            threadRef{Pupil: kid.PupilID, Conversation: g.ConversationID}.String(),
					Kind:          kindGroup,
					PupilID:       kid.PupilID,
					Child:         kid.Name,
					Title:         g.Title,
					Preview:       preview(g.LastMessage),
					LastMessageAt: stamp(g.LastMessageDate.Time),
					Unread:        g.UnreadMessageCount,
					ReadOnly:      g.IsReadonly,
				})
			}
		}
	}
	if c.Bool("unread") {
		threads = slices.DeleteFunc(threads, func(t thread) bool { return t.Unread == 0 })
	}
	// The service returns direct threads in no order. Latest first.
	slices.SortStableFunc(threads, func(x, y thread) int { return cmp.Compare(y.LastMessageAt, x.LastMessageAt) })

	list := threadList{Items: threads}
	if len(threads) > limit {
		list.Items, list.Truncated = threads[:limit], true
	}
	return c.Print(list, func(w io.Writer) { renderThreads(w, list) })
}

// message is one message as thread prints it.
type message struct {
	SentAt string `json:"sentAt"`
	// From is the sender's name. In a direct thread it is the employee, or
	// "me".
	From string `json:"from"`
	Mine bool   `json:"mine"`
	// ReadAt is when a direct message was read by the one it was sent to.
	ReadAt      string       `json:"readAt,omitempty"`
	Title       string       `json:"title,omitempty"`
	Text        string       `json:"text"`
	Attachments []attachment `json:"attachments"`
}

type attachment struct {
	ID          string `json:"id"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
}

// threadDetail is what thread prints. Items holds the latest messages,
// oldest first.
type threadDetail struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
	// Participants is everyone in a group conversation, and the one
	// employee of a direct thread.
	Participants []string  `json:"participants"`
	CanReply     bool      `json:"canReply"`
	Items        []message `json:"items"`
	Truncated    bool      `json:"truncated"`
}

func (a *App) thread(ctx context.Context, c *ctlkit.Call) error {
	limit := c.Int("limit")
	if limit < 1 || limit > maxLimit {
		return ctlkit.Usagef("--limit takes 1 to %d, not %d", maxLimit, limit)
	}
	ref, err := parseThreadRef(c.Arg(0))
	if err != nil {
		return err
	}
	if err := a.authorize(ctx); err != nil {
		return err
	}
	detail, err := a.loadThread(ctx, ref)
	if err != nil {
		return err
	}
	if len(detail.Items) > limit {
		detail.Items, detail.Truncated = detail.Items[len(detail.Items)-limit:], true
	}
	return c.Print(detail, func(w io.Writer) { renderThread(w, detail) })
}

// loadThread fetches a thread with every message, oldest first.
func (a *App) loadThread(ctx context.Context, ref threadRef) (threadDetail, error) {
	detail := threadDetail{ID: ref.String(), Kind: ref.kind(), Participants: []string{}, Items: []message{}}
	if ref.Conversation == "" {
		conversation, err := a.Client.Direct(ctx, ref.Pupil, ref.Employee)
		if err != nil {
			return detail, threadError(err, ref)
		}
		detail.CanReply = conversation.CanReply
		// A direct thread does not say who it is with. The list does.
		threads, err := a.Client.DirectThreads(ctx, ref.Pupil)
		if err != nil {
			return detail, err
		}
		for _, t := range threads {
			if t.EmployeeID == ref.Employee {
				detail.Participants = append(detail.Participants, t.Sender)
			}
		}
		for _, m := range conversation.Messages {
			mine := m.ParticipantType == flyt.FromGuardian
			from := cmp.Or(strings.Join(detail.Participants, ", "), "school")
			if mine {
				from = "me"
			}
			detail.Items = append(detail.Items, message{
				SentAt:      stamp(m.SentDate.Time),
				From:        from,
				Mine:        mine,
				ReadAt:      stamp(m.ReadDate.Time),
				Title:       m.Title,
				Text:        m.MessageText,
				Attachments: attachments(m.Attachments),
			})
		}
	} else {
		conversation, err := a.Client.Group(ctx, ref.Pupil, ref.Conversation)
		if err != nil {
			return detail, threadError(err, ref)
		}
		detail.Title = conversation.Title
		detail.CanReply = !conversation.IsReadonly
		detail.Participants = append(detail.Participants, conversation.Participants...)
		for _, m := range conversation.Messages {
			detail.Items = append(detail.Items, message{
				SentAt:      stamp(m.SentOn.Time),
				From:        m.SentBy,
				Mine:        m.SentByMe,
				Text:        m.Message,
				Attachments: attachments(m.Attachments),
			})
		}
	}
	slices.SortStableFunc(detail.Items, func(x, y message) int { return cmp.Compare(x.SentAt, y.SentAt) })
	return detail, nil
}

func attachments(files []flyt.Attachment) []attachment {
	list := []attachment{}
	for _, f := range files {
		list = append(list, attachment{ID: f.AttachmentID, FileName: f.FileName, ContentType: f.ContentType, Bytes: f.ContentLength})
	}
	return list
}

// replyResult is what reply prints. With DryRun set, nothing was sent and
// Request is what would have been.
type replyResult struct {
	DryRun   bool              `json:"dryRun"`
	ThreadID string            `json:"threadId"`
	Request  flyt.ReplyRequest `json:"request"`
	Sent     bool              `json:"sent"`
}

func (a *App) reply(ctx context.Context, c *ctlkit.Call) error {
	ref, err := parseThreadRef(c.Arg(0))
	if err != nil {
		return err
	}
	result := replyResult{ThreadID: ref.String(), Request: flyt.ReplyRequest{Text: strings.TrimSpace(c.Arg(1))}}
	if result.Request.Text == "" {
		return ctlkit.Usagef("the text must not be empty")
	}
	if err := a.authorize(ctx); err != nil {
		return err
	}
	// The thread is read first, so that the question can name who gets the
	// message and a thread that takes no reply is refused before it is asked.
	detail, err := a.loadThread(ctx, ref)
	if err != nil {
		return err
	}
	if !detail.CanReply {
		return ctlkit.Errorf(ctlkit.KindConflict, "thread %s does not take replies", ref)
	}
	if c.DryRun() {
		result.DryRun = true
		return c.Print(result, func(w io.Writer) { renderReply(w, result, detail) })
	}
	if err := c.Confirm(fmt.Sprintf("Send this message to %s?\n\n%s\n", threadName(detail), result.Request.Text)); err != nil {
		return err
	}
	if ref.Conversation == "" {
		err = a.Client.ReplyDirect(ctx, ref.Pupil, ref.Employee, result.Request)
	} else {
		err = a.Client.ReplyGroup(ctx, ref.Pupil, ref.Conversation, result.Request)
	}
	if err != nil {
		return threadError(err, ref)
	}
	result.Sent = true
	return c.Print(result, func(w io.Writer) { renderReply(w, result, detail) })
}

// markReadResult is what mark-read prints.
type markReadResult struct {
	DryRun   bool   `json:"dryRun"`
	ThreadID string `json:"threadId"`
	Marked   bool   `json:"marked"`
}

func (a *App) markRead(ctx context.Context, c *ctlkit.Call) error {
	ref, err := parseThreadRef(c.Arg(0))
	if err != nil {
		return err
	}
	result := markReadResult{ThreadID: ref.String()}
	if c.DryRun() {
		result.DryRun = true
		return c.Print(result, func(w io.Writer) { renderMarkRead(w, result) })
	}
	if err := a.authorize(ctx); err != nil {
		return err
	}
	if err := c.Confirm(fmt.Sprintf("Mark thread %s as read? The school can see that you read it.", ref)); err != nil {
		return err
	}
	if ref.Conversation == "" {
		err = a.Client.MarkDirectRead(ctx, ref.Pupil, ref.Employee)
	} else {
		err = a.Client.MarkGroupRead(ctx, ref.Pupil, ref.Conversation)
	}
	if err != nil {
		return threadError(err, ref)
	}
	result.Marked = true
	return c.Print(result, func(w io.Writer) { renderMarkRead(w, result) })
}

// threadName says who a thread is with, for a question or a result.
func threadName(detail threadDetail) string {
	who := oneLine(strings.Join(detail.Participants, ", "))
	switch {
	case who == "":
		return "thread " + detail.ID
	case detail.Title == "":
		return who
	}
	return fmt.Sprintf("%q (%s)", oneLine(detail.Title), who)
}

// threadError tells where the ids come from. The service answers an id it
// does not know with a 500, and a pupil that is not the guardian's with a 403.
func threadError(err error, ref threadRef) error {
	switch ctlkit.KindOf(err) {
	case ctlkit.KindUnavailable, ctlkit.KindNotFound:
		return ctlkit.Errorf(ctlkit.KindOf(err), "%w", err).WithHint(`the service answers like this for a thread id it does not know. Run "%s threads" to see the ids.`, Name)
	}
	return err
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// preview cuts a text to the length the service uses for a direct thread.
func preview(text string) string {
	const length = 80
	runes := []rune(oneLine(text))
	if len(runes) <= length {
		return string(runes)
	}
	return string(runes[:length]) + "..."
}
