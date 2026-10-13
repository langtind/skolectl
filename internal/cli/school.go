package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/langtind/ctlkit"

	"github.com/langtind/skolectl/internal/flyt"
)

var childFlag = ctlkit.Flag{Name: "child", Kind: ctlkit.Int, Value: "pupil-id", Help: "only this child, by the pupil id that children prints"}

// pick returns the children a command is about: every child at a school,
// or the one --child names.
func (a *App) pick(ctx context.Context, c *ctlkit.Call) ([]child, error) {
	children, err := a.schoolChildren(ctx)
	if err != nil {
		return nil, err
	}
	if c.Has("child") && c.Given("child") {
		want := c.Int("child")
		children = slices.DeleteFunc(children, func(k child) bool { return k.PupilID != want })
		if len(children) == 0 {
			return nil, ctlkit.NotFoundf("no child with pupil id %d", want).WithHint(`run "%s children" to see the ids.`, Name)
		}
	}
	return children, nil
}

// pupilRef splits an id of the form "<pupil>.<number>", which forms and
// applications use.
func pupilRef(id, list string) (pupil, number int, err error) {
	left, right, ok := strings.Cut(id, ".")
	pupil, errPupil := strconv.Atoi(left)
	number, errNumber := strconv.Atoi(right)
	if !ok || errPupil != nil || errNumber != nil {
		return 0, 0, ctlkit.Usagef("%q is not an id that %s prints", id, list).WithHint(`run "%s %s" to see the ids.`, Name, list)
	}
	return pupil, number, nil
}

// overviewItem is what waits for the guardian, for one child.
type overviewItem struct {
	PupilID             int    `json:"pupilId"`
	Child               string `json:"child"`
	UnreadDirect        int    `json:"unreadDirectMessages"`
	UnreadGroup         int    `json:"unreadGroupMessages"`
	UnreadNotices       int    `json:"unreadNotices"`
	UnansweredForms     int    `json:"unansweredForms"`
	UnansweredConsents  int    `json:"unansweredConsents"`
	WaitingApplications int    `json:"waitingApplications"`
}

type overview struct {
	Items []overviewItem `json:"items"`
}

func (a *App) overview(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}
	result := overview{Items: []overviewItem{}}
	for _, kid := range children {
		item := overviewItem{PupilID: kid.PupilID, Child: kid.Name}
		info, err := a.Client.MessageInfo(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		item.UnreadDirect, item.UnreadGroup = info.DirectUnread, info.GroupUnread
		for _, kind := range []int{flyt.NoticeNotification, flyt.NoticeAnnouncement} {
			notices, err := a.Client.Notices(ctx, kid.PupilID, kind)
			if err != nil {
				return err
			}
			for _, n := range notices {
				if n.IsUnread {
					item.UnreadNotices++
				}
			}
		}
		forms, err := a.Client.Forms(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		for _, f := range forms {
			if !f.IsAnswered && !f.IsDeadlinePassed {
				item.UnansweredForms++
			}
		}
		consents, err := a.Client.Consents(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		for _, k := range consents {
			if k.Status == flyt.ConsentNotAnswered {
				item.UnansweredConsents++
			}
		}
		applications, err := a.Client.Applications(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		for _, application := range applications {
			if application.Status >= 1 && application.Status <= 3 {
				item.WaitingApplications++
			}
		}
		result.Items = append(result.Items, item)
	}
	return c.Print(result, func(w io.Writer) { renderOverview(w, result) })
}

const (
	noticeNotification = "notification"
	noticeAnnouncement = "announcement"
)

// notice is one notice as notices prints it.
type notice struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	PupilID int    `json:"pupilId"`
	Child   string `json:"child"`
	SentAt  string `json:"sentAt"`
	Sender  string `json:"sender"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Unread  bool   `json:"unread"`
}

type noticeList struct {
	Items     []notice `json:"items"`
	Truncated bool     `json:"truncated"`
}

func (a *App) notices(ctx context.Context, c *ctlkit.Call) error {
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
	kinds := map[string]int{noticeNotification: flyt.NoticeNotification, noticeAnnouncement: flyt.NoticeAnnouncement}
	if want := c.String("kind"); want != "" {
		kinds = map[string]int{want: kinds[want]}
	}
	list := noticeList{Items: []notice{}}
	for _, kid := range children {
		for name, kind := range kinds {
			notices, err := a.Client.Notices(ctx, kid.PupilID, kind)
			if err != nil {
				return err
			}
			for _, n := range notices {
				if c.Bool("unread") && !n.IsUnread {
					continue
				}
				list.Items = append(list.Items, notice{
					ID: n.ID, Kind: name, PupilID: kid.PupilID, Child: kid.Name, SentAt: stamp(n.SentDate.Time),
					Sender: n.Sender, Title: n.Title, Text: n.MessageText, Unread: n.IsUnread,
				})
			}
		}
	}
	slices.SortStableFunc(list.Items, func(x, y notice) int {
		return cmp.Or(cmp.Compare(y.SentAt, x.SentAt), cmp.Compare(x.ID, y.ID))
	})
	if len(list.Items) > limit {
		list.Items, list.Truncated = list.Items[:limit], true
	}
	return c.Print(list, func(w io.Writer) { renderNotices(w, list) })
}

// absence is one absence as absences prints it.
type absence struct {
	Date       string `json:"date"`
	Kind       string `json:"kind"`
	Documented bool   `json:"documented"`
	Days       int    `json:"days"`
	Comment    string `json:"comment,omitempty"`
	ModifiedBy string `json:"modifiedBy,omitempty"`
}

// absenceReport is the absence of one child in one school year or term.
type absenceReport struct {
	PupilID    int                 `json:"pupilId"`
	Child      string              `json:"child"`
	SchoolYear string              `json:"schoolYear"`
	Term       string              `json:"term"`
	Summary    flyt.AbsenceSummary `json:"summary"`
	Absences   []absence           `json:"absences"`
}

type absenceList struct {
	Items []absenceReport `json:"items"`
}

var terms = map[string]int{"": 0, "fall": flyt.TermFall, "spring": flyt.TermSpring}

func (a *App) absences(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}
	term := c.String("term")
	list := absenceList{Items: []absenceReport{}}
	for _, kid := range children {
		years, err := a.Client.AbsenceYears(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		at := slices.IndexFunc(years, func(y flyt.SchoolYear) bool { return y.IsCurrent })
		if at < 0 {
			continue
		}
		year := years[at]
		summary, err := a.Client.AbsenceSummary(ctx, kid.PupilID, year.ID, terms[term])
		if err != nil {
			return err
		}
		absences, err := a.Client.Absences(ctx, kid.PupilID, year.ID, terms[term])
		if err != nil {
			return err
		}
		report := absenceReport{
			PupilID: kid.PupilID, Child: kid.Name, SchoolYear: fmt.Sprintf("%d/%d", year.FromYear, year.ToYear),
			Term: cmp.Or(term, "all year"), Summary: *summary, Absences: []absence{},
		}
		for _, entry := range absences {
			kind := "hours"
			if entry.Type == 1 {
				kind = "day"
			}
			report.Absences = append(report.Absences, absence{
				Date: entry.Date.Format("2006-01-02"), Kind: kind, Documented: entry.Status == 1,
				Days: entry.DaysAbsent, Comment: entry.Comment, ModifiedBy: entry.ModifiedBy,
			})
		}
		slices.SortStableFunc(report.Absences, func(x, y absence) int { return cmp.Compare(y.Date, x.Date) })
		list.Items = append(list.Items, report)
	}
	return c.Print(list, func(w io.Writer) { renderAbsences(w, list) })
}

// form is one form as forms prints it.
type form struct {
	ID             string `json:"id"`
	PupilID        int    `json:"pupilId"`
	Child          string `json:"child"`
	Title          string `json:"title"`
	PublishedAt    string `json:"publishedAt,omitempty"`
	Deadline       string `json:"deadline,omitempty"`
	Answered       bool   `json:"answered"`
	DeadlinePassed bool   `json:"deadlinePassed"`
}

type formList struct {
	Items []form `json:"items"`
}

func (a *App) forms(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}
	list := formList{Items: []form{}}
	for _, kid := range children {
		forms, err := a.Client.Forms(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		for _, f := range forms {
			if c.Bool("unanswered") && (f.IsAnswered || f.IsDeadlinePassed) {
				continue
			}
			list.Items = append(list.Items, form{
				ID: fmt.Sprintf("%d.%d", kid.PupilID, f.ID), PupilID: kid.PupilID, Child: kid.Name, Title: f.Title,
				PublishedAt: stamp(f.PublishedOn.Time), Deadline: stamp(f.Deadline.Time),
				Answered: f.IsAnswered, DeadlinePassed: f.IsDeadlinePassed,
			})
		}
	}
	slices.SortStableFunc(list.Items, func(x, y form) int { return cmp.Compare(y.PublishedAt, x.PublishedAt) })
	return c.Print(list, func(w io.Writer) { renderForms(w, list) })
}

type alternative struct {
	Text     string `json:"text"`
	Selected bool   `json:"selected"`
}

// formDetail is what form prints.
type formDetail struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Text         string        `json:"text"`
	MoreInfo     string        `json:"moreInfo,omitempty"`
	PublishedAt  string        `json:"publishedAt,omitempty"`
	Deadline     string        `json:"deadline,omitempty"`
	Answered     bool          `json:"answered"`
	AnsweredBy   string        `json:"answeredBy,omitempty"`
	AnsweredAt   string        `json:"answeredAt,omitempty"`
	ReadOnly     bool          `json:"readOnly"`
	Alternatives []alternative `json:"alternatives"`
}

func (a *App) form(ctx context.Context, c *ctlkit.Call) error {
	pupil, id, err := pupilRef(c.Arg(0), "forms")
	if err != nil {
		return err
	}
	if err := a.authorize(ctx); err != nil {
		return err
	}
	f, err := a.Client.Form(ctx, pupil, id)
	if err != nil {
		return idError(err, "forms")
	}
	detail := formDetail{
		ID: c.Arg(0), Title: f.Title, Text: f.PublishText, MoreInfo: f.FurtherInformation,
		PublishedAt: stamp(f.PublishedOn.Time), Deadline: stamp(f.Deadline.Time),
		Answered: f.IsAnswered, ReadOnly: f.IsReadOnly, Alternatives: []alternative{},
	}
	chosen := map[int]bool{}
	if f.Answers != nil {
		detail.AnsweredBy, detail.AnsweredAt = f.Answers.LastModifiedBy, stamp(f.Answers.LastModifiedOn.Time)
		for _, answer := range f.Answers.Answers {
			chosen[answer.AlternativeID] = true
		}
	}
	slices.SortStableFunc(f.Alternatives, func(x, y flyt.FormAlternative) int { return cmp.Compare(x.Order, y.Order) })
	for _, alt := range f.Alternatives {
		detail.Alternatives = append(detail.Alternatives, alternative{Text: alt.Text, Selected: chosen[alt.ID]})
	}
	return c.Print(detail, func(w io.Writer) { renderForm(w, detail) })
}

// consent is one consent as consents prints it.
type consent struct {
	PupilID    int    `json:"pupilId"`
	Child      string `json:"child"`
	Title      string `json:"title"`
	Answer     string `json:"answer"`
	Text       string `json:"text"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	ModifiedBy string `json:"modifiedBy,omitempty"`
}

type consentList struct {
	Items []consent `json:"items"`
}

var consentAnswers = map[int]string{flyt.ConsentNotAnswered: "not answered", flyt.ConsentYes: "yes", flyt.ConsentNo: "no"}

func (a *App) consents(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}
	list := consentList{Items: []consent{}}
	for _, kid := range children {
		consents, err := a.Client.Consents(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		for _, k := range consents {
			list.Items = append(list.Items, consent{
				PupilID: kid.PupilID, Child: kid.Name, Title: k.Title, Answer: numberName(consentAnswers, k.Status),
				Text: k.Content, ModifiedAt: stamp(k.ModifiedOn.Time), ModifiedBy: k.ModifiedBy,
			})
		}
	}
	return c.Print(list, func(w io.Writer) { renderConsents(w, list) })
}

// application is one application as applications and application print it.
type application struct {
	ID         string `json:"id"`
	PupilID    int    `json:"pupilId"`
	Child      string `json:"child,omitempty"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	CreatedAt  string `json:"createdAt"`
	AnsweredBy string `json:"answeredBy,omitempty"`
	AnsweredAt string `json:"answeredAt,omitempty"`

	RequestedBy string `json:"requestedBy,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	Cause       string `json:"cause,omitempty"`
	Comment     string `json:"comment,omitempty"`
	Answer      string `json:"answer,omitempty"`
}

type applicationList struct {
	Items []application `json:"items"`
}

func numberName(names map[int]string, number int) string {
	if name, ok := names[number]; ok {
		return name
	}
	return fmt.Sprintf("unknown (%d)", number)
}

func date(t flyt.LocalTime) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func toApplication(pupil int, kid string, from flyt.Application) application {
	return application{
		ID: fmt.Sprintf("%d.%d", pupil, from.RequestID), PupilID: pupil, Child: kid,
		Type: numberName(flyt.ApplicationTypes, from.ApplicationType), Status: numberName(flyt.ApplicationStatuses, from.Status),
		CreatedAt: stamp(from.CreatedDate.Time), AnsweredBy: from.AnsweredBy, AnsweredAt: stamp(from.AnsweredTime.Time),
		RequestedBy: from.RequestedBy, From: date(from.FromDate), To: date(from.ToDate),
		Cause: from.CauseName, Comment: from.ParentComment, Answer: from.EmployeeComment,
	}
}

func (a *App) applications(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}
	list := applicationList{Items: []application{}}
	for _, kid := range children {
		applications, err := a.Client.Applications(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		for _, entry := range applications {
			list.Items = append(list.Items, toApplication(kid.PupilID, kid.Name, entry))
		}
	}
	slices.SortStableFunc(list.Items, func(x, y application) int { return cmp.Compare(y.CreatedAt, x.CreatedAt) })
	return c.Print(list, func(w io.Writer) { renderApplications(w, list) })
}

func (a *App) application(ctx context.Context, c *ctlkit.Call) error {
	pupil, id, err := pupilRef(c.Arg(0), "applications")
	if err != nil {
		return err
	}
	if err := a.authorize(ctx); err != nil {
		return err
	}
	entry, err := a.Client.Application(ctx, pupil, id)
	if err != nil {
		return idError(err, "applications")
	}
	result := toApplication(pupil, "", *entry)
	return c.Print(result, func(w io.Writer) { renderApplication(w, result) })
}

type subjectGroup struct {
	Subject  string   `json:"subject"`
	Group    string   `json:"group"`
	Teachers []string `json:"teachers"`
}

// class is the class of one child and who teaches it.
type class struct {
	PupilID        int            `json:"pupilId"`
	Child          string         `json:"child"`
	Class          string         `json:"class,omitempty"`
	ContactTeacher string         `json:"contactTeacher,omitempty"`
	Teachers       []string       `json:"teachers"`
	Subjects       []subjectGroup `json:"subjects"`
}

type classList struct {
	Items []class `json:"items"`
}

func (a *App) classes(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}
	list := classList{Items: []class{}}
	for _, kid := range children {
		groups, err := a.Client.Groups(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		entry := class{PupilID: kid.PupilID, Child: kid.Name, Teachers: []string{}, Subjects: []subjectGroup{}}
		if groups.ContactTeacher != nil {
			entry.ContactTeacher = groups.ContactTeacher.FullName
		}
		if groups.ClassGroup != nil {
			entry.Class = groups.ClassGroup.Name
			for _, teacher := range groups.ClassGroup.Responsibles {
				entry.Teachers = append(entry.Teachers, teacher.FullName)
			}
		}
		for _, group := range groups.SubjectGroups {
			subject := subjectGroup{Subject: group.Subject.Name, Group: group.Name, Teachers: []string{}}
			for _, teacher := range group.Responsibles {
				subject.Teachers = append(subject.Teachers, teacher.FullName)
			}
			entry.Subjects = append(entry.Subjects, subject)
		}
		list.Items = append(list.Items, entry)
	}
	return c.Print(list, func(w io.Writer) { renderClasses(w, list) })
}

// school is how to reach one school, and the links it lists.
type school struct {
	Name     string      `json:"name"`
	Phone    string      `json:"phone,omitempty"`
	Email    string      `json:"email,omitempty"`
	Address  string      `json:"address,omitempty"`
	Homepage string      `json:"homepage,omitempty"`
	Links    []flyt.Link `json:"links"`
}

type schoolList struct {
	Items []school `json:"items"`
}

// schoolIDs lists the schools of the children, each once.
func schoolIDs(children []child) []string {
	ids := []string{}
	for _, kid := range children {
		if kid.SchoolID != "" && !slices.Contains(ids, kid.SchoolID) {
			ids = append(ids, kid.SchoolID)
		}
	}
	return ids
}

func (a *App) schools(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}
	list := schoolList{Items: []school{}}
	for _, id := range schoolIDs(children) {
		info, err := a.Client.School(ctx, id)
		if err != nil {
			return err
		}
		links, err := a.Client.SchoolLinks(ctx, id)
		if err != nil {
			return err
		}
		entry := school{
			Name: info.Name, Phone: info.PhoneNumber, Email: info.EmailAddress, Homepage: info.Homepage,
			Address: strings.TrimSpace(strings.Trim(info.Street+", "+strings.TrimSpace(info.Zip+" "+info.City), ", ")),
			Links:   []flyt.Link{},
		}
		entry.Links = append(entry.Links, links...)
		list.Items = append(list.Items, entry)
	}
	return c.Print(list, func(w io.Writer) { renderSchools(w, list) })
}

// document is one file as documents prints it. Its id starts with "p" for a
// file about one child and with "s" for one the school shares with everyone.
type document struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	Child     string `json:"child,omitempty"`
	Title     string `json:"title"`
	School    string `json:"school,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	CreatedBy string `json:"createdBy,omitempty"`
}

type documentList struct {
	Items []document `json:"items"`
}

func (a *App) documents(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}
	list := documentList{Items: []document{}}
	for _, kid := range children {
		// The service lists a child's files one school year at a time.
		years, err := a.Client.DocumentYears(ctx, kid.PupilID)
		if err != nil {
			return err
		}
		for _, year := range years {
			files, err := a.Client.PupilDocuments(ctx, kid.PupilID, year.ID)
			if err != nil {
				return err
			}
			for _, file := range files {
				list.Items = append(list.Items, document{
					ID: fmt.Sprintf("p.%d.%s", kid.PupilID, file.ArchiveFileID), Scope: "child", Child: kid.Name,
					Title: file.Title, School: file.SchoolName, CreatedAt: stamp(file.CreatedOn.Time), CreatedBy: file.CreatedBy,
				})
			}
		}
	}
	slices.SortStableFunc(list.Items, func(x, y document) int { return cmp.Compare(y.CreatedAt, x.CreatedAt) })
	for _, id := range schoolIDs(children) {
		files, err := a.Client.SchoolDocuments(ctx, id)
		if err != nil {
			return err
		}
		for _, file := range files {
			list.Items = append(list.Items, document{ID: fmt.Sprintf("s.%s.%d", id, file.DocumentID), Scope: "school", Title: file.Title})
		}
	}
	return c.Print(list, func(w io.Writer) { renderDocuments(w, list) })
}

// saved is what a command that fetches a file prints.
type saved struct {
	Path        string `json:"path"`
	Bytes       int    `json:"bytes"`
	ContentType string `json:"contentType"`
}

func (a *App) document(ctx context.Context, c *ctlkit.Call) error {
	bad := ctlkit.Usagef("%q is not a document id", c.Arg(0)).WithHint(`run "%s documents" to see the ids.`, Name)
	parts := strings.SplitN(c.Arg(0), ".", 3)
	if len(parts) != 3 {
		return bad
	}
	if err := a.authorize(ctx); err != nil {
		return err
	}
	var data []byte
	var err error
	switch parts[0] {
	case "p":
		pupil, convErr := strconv.Atoi(parts[1])
		if convErr != nil {
			return bad
		}
		data, err = a.Client.PupilDocumentFile(ctx, pupil, parts[2])
	case "s":
		number, convErr := strconv.Atoi(parts[2])
		if convErr != nil {
			return bad
		}
		data, err = a.Client.SchoolDocumentFile(ctx, parts[1], number)
	default:
		return bad
	}
	if err != nil {
		return idError(err, "documents")
	}
	return a.save(c, data, "document-"+parts[2])
}

func (a *App) attachment(ctx context.Context, c *ctlkit.Call) error {
	ref, err := parseThreadRef(c.Arg(0))
	if err != nil {
		return err
	}
	if ref.Conversation == "" {
		return ctlkit.Usagef("a file on a direct thread cannot be fetched yet: none has been seen, so the request is not known")
	}
	if err := a.authorize(ctx); err != nil {
		return err
	}
	// The thread is read for the name of the file.
	detail, err := a.loadThread(ctx, ref)
	if err != nil {
		return err
	}
	name := ""
	for _, m := range detail.Items {
		for _, file := range m.Attachments {
			if file.ID == c.Arg(1) {
				name = file.FileName
			}
		}
	}
	if name == "" {
		return ctlkit.NotFoundf("thread %s has no attachment %s", ref, c.Arg(1)).WithHint(`run "%s thread %s" to see the attachments.`, Name, ref)
	}
	data, err := a.Client.GroupAttachment(ctx, ref.Pupil, ref.Conversation, c.Arg(1))
	if err != nil {
		return err
	}
	return a.save(c, data, name)
}

// extensions names a file that came without a name, by what it holds.
var extensions = map[string]string{
	"application/pdf": ".pdf", "image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
	"application/zip": ".zip", "text/plain; charset=utf-8": ".txt",
}

// save writes a fetched file to --output, or to name in the current
// directory. A name from the service is cut down to its last element, and a
// file that exists is never replaced.
func (a *App) save(c *ctlkit.Call, data []byte, name string) error {
	contentType := http.DetectContentType(data)
	path := c.String("output")
	if path == "" {
		path = filepath.Base(filepath.Clean("/" + strings.ReplaceAll(name, `\`, "/")))
		if filepath.Ext(path) == "" {
			path += extensions[contentType]
		}
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return ctlkit.Errorf(ctlkit.KindConflict, "%s exists already", path).WithHint("give another path with --output.")
	}
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	result := saved{Path: path, Bytes: len(data), ContentType: contentType}
	return c.Print(result, func(w io.Writer) {
		fmt.Fprintf(w, "Saved %s (%d bytes, %s).\n", result.Path, result.Bytes, result.ContentType)
	})
}

// idError tells where the ids come from. The service answers an id it does
// not know with a 500 or a 403.
func idError(err error, list string) error {
	switch ctlkit.KindOf(err) {
	case ctlkit.KindUnavailable, ctlkit.KindNotFound, ctlkit.KindAuth:
		return ctlkit.Errorf(ctlkit.KindOf(err), "%w", err).WithHint(`the service answers like this for an id it does not know. Run "%s %s" to see the ids.`, Name, list)
	}
	return err
}
