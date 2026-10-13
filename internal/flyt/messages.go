package flyt

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Oslo is the zone the service writes its times in. Message times come
// without a zone, see reference/README.md.
var Oslo = func() *time.Location {
	zone, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		return time.FixedZone("CET", 3600)
	}
	return zone
}()

// LocalTime is a time the service wrote without a zone, read as Oslo time.
// It is the zero time for null and for a value that is not a time.
type LocalTime struct{ time.Time }

func (t *LocalTime) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "" || text == "null" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"} {
		if parsed, err := time.ParseInLocation(layout, text, Oslo); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return nil
}

// Child is one child of the guardian that goes to a school.
type Child struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	School    *struct {
		PupilID          int    `json:"pupilId"`
		SchoolID         string `json:"schoolId"`
		Level            int    `json:"level"`
		OrganizationName string `json:"organizationName"`
	} `json:"school"`
}

// Children lists the children. One without a school has no messages.
func (c *Client) Children(ctx context.Context) ([]Child, error) {
	var answer struct {
		Children []Child `json:"children"`
	}
	err := c.do(ctx, http.MethodGet, "v2/children", nil, &answer)
	return answer.Children, err
}

// DirectThread is the latest message between the guardian and one employee.
type DirectThread struct {
	Title       string    `json:"title"`
	MessageText string    `json:"messageText"`
	Sender      string    `json:"sender"`
	EmployeeID  int       `json:"employeeId"`
	SentDate    LocalTime `json:"sentDate"`
	UnreadCount int       `json:"unreadCount"`
}

// DirectThreads lists one row for each employee the guardian has messages
// with. The service returns them in no order.
func (c *Client) DirectThreads(ctx context.Context, pupil int) ([]DirectThread, error) {
	var threads []DirectThread
	err := c.do(ctx, http.MethodGet, pupilPath(pupil, "direct"), nil, &threads)
	return threads, err
}

// The two values of DirectMessage.ParticipantType. The web client calls the
// first "receiver": the guardian received the message.
const (
	FromSchool   = 1
	FromGuardian = 2
)

type DirectMessage struct {
	ID              string       `json:"idMessage"`
	Title           string       `json:"title"`
	MessageText     string       `json:"messageText"`
	SentDate        LocalTime    `json:"sentDate"`
	ReadDate        LocalTime    `json:"readDate"`
	ParticipantType int          `json:"participantType"`
	Attachments     []Attachment `json:"attachments"`
}

type DirectConversation struct {
	// Messages come latest first.
	Messages []DirectMessage `json:"messages"`
	CanReply bool            `json:"canReply"`
}

// Direct returns every message between the guardian and one employee.
// Reading it does not mark anything as read.
func (c *Client) Direct(ctx context.Context, pupil, employee int) (*DirectConversation, error) {
	var conversation DirectConversation
	path := fmt.Sprintf("vfs/v2/pupils/%d/messages/direct/%d", pupil, employee)
	err := c.do(ctx, http.MethodGet, path, nil, &conversation)
	return &conversation, err
}

// GroupThread is one conversation with several receivers.
type GroupThread struct {
	ConversationID     string    `json:"conversationId"`
	Title              string    `json:"title"`
	ConcerningPupils   string    `json:"concerningPupils"`
	LastMessage        string    `json:"lastMessage"`
	LastMessageDate    LocalTime `json:"lastMessageDate"`
	UnreadMessageCount int       `json:"unreadMessageCount"`
	IsReadonly         bool      `json:"isReadonly"`
}

// GroupThreads lists the group conversations, latest first.
func (c *Client) GroupThreads(ctx context.Context, pupil int) ([]GroupThread, error) {
	var threads []GroupThread
	err := c.do(ctx, http.MethodGet, pupilPath(pupil, "group"), nil, &threads)
	return threads, err
}

type GroupMessage struct {
	SentOn           LocalTime    `json:"sentOn"`
	SentBy           string       `json:"sentBy"`
	Message          string       `json:"message"`
	SentByMe         bool         `json:"sentByMe"`
	IsRemovalMessage bool         `json:"isRemovalMessage"`
	Attachments      []Attachment `json:"attachments"`
}

type GroupConversation struct {
	ConversationID string   `json:"conversationId"`
	Title          string   `json:"title"`
	IsReadonly     bool     `json:"isReadonly"`
	Participants   []string `json:"participants"`
	// Messages come in the order the service returns them. The caller sorts.
	Messages []GroupMessage `json:"messages"`
}

// Group returns one group conversation with its messages. Reading it does
// not mark anything as read.
func (c *Client) Group(ctx context.Context, pupil int, conversation string) (*GroupConversation, error) {
	var answer GroupConversation
	err := c.do(ctx, http.MethodGet, pupilPath(pupil, "group/"+conversation), nil, &answer)
	return &answer, err
}

// Attachment is a file on a message. Only a group conversation has been
// seen with one.
type Attachment struct {
	AttachmentID  string `json:"attachmentId"`
	FileName      string `json:"fileName"`
	ContentType   string `json:"contentType"`
	ContentLength int    `json:"contentLength"`
}

// ReplyRequest is what a reply sends, as the JSON in the form field
// "request". A dry run prints it.
type ReplyRequest struct {
	Text string `json:"text"`
}

// ReplyDirect sends a message to the employee of a direct thread. Running
// it again sends one more message.
func (c *Client) ReplyDirect(ctx context.Context, pupil, employee int, request ReplyRequest) error {
	return c.do(ctx, http.MethodPost, pupilPath(pupil, fmt.Sprintf("direct/%d/reply", employee)), formRequest{request}, nil)
}

// ReplyGroup sends a message to a group conversation. Running it again
// sends one more message.
func (c *Client) ReplyGroup(ctx context.Context, pupil int, conversation string, request ReplyRequest) error {
	return c.do(ctx, http.MethodPost, pupilPath(pupil, "group/"+conversation+"/reply"), formRequest{request}, nil)
}

// MarkDirectRead marks every message from one employee as read. The
// employee can see when. Running it again changes nothing.
func (c *Client) MarkDirectRead(ctx context.Context, pupil, employee int) error {
	return c.do(ctx, http.MethodPost, pupilPath(pupil, fmt.Sprintf("direct/%d/mark-as-read", employee)), nil, nil)
}

// MarkGroupRead marks a group conversation as read. The other participants
// can see it. Running it again changes nothing.
func (c *Client) MarkGroupRead(ctx context.Context, pupil int, conversation string) error {
	return c.do(ctx, http.MethodPost, pupilPath(pupil, "group/"+conversation+"/mark-as-read"), nil, nil)
}

func pupilPath(pupil int, rest string) string {
	return fmt.Sprintf("vfs/v1/pupils/%d/messages/%s", pupil, rest)
}
