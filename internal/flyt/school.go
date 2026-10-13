package flyt

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// MessageInfo counts the unread messages of one child.
type MessageInfo struct {
	DirectUnread int `json:"directMessagesUnreadCount"`
	GroupUnread  int `json:"groupMessagesUnreadCount"`
}

func (c *Client) MessageInfo(ctx context.Context, pupil int) (*MessageInfo, error) {
	var info MessageInfo
	err := c.do(ctx, http.MethodGet, pupilPath(pupil, "message-info"), nil, &info)
	return &info, err
}

// GroupAttachment fetches a file from a group conversation.
func (c *Client) GroupAttachment(ctx context.Context, pupil int, conversation, attachment string) ([]byte, error) {
	return c.fetch(ctx, http.MethodGet, pupilPath(pupil, "group/"+conversation+"/attachment/"+url.PathEscape(attachment)), nil)
}

// The two kinds of notice. The web client calls kind 1 an announcement and
// kind 0 a notification.
const (
	NoticeNotification = 0
	NoticeAnnouncement = 1
)

// Notice is a one-way message from the school.
type Notice struct {
	ID          string    `json:"idMessage"`
	SentDate    LocalTime `json:"sentDate"`
	Title       string    `json:"title"`
	Sender      string    `json:"sender"`
	MessageText string    `json:"messageText"`
	IsUnread    bool      `json:"isUnread"`
}

// Notices lists the notices of one kind, in the order the service returns.
func (c *Client) Notices(ctx context.Context, pupil, kind int) ([]Notice, error) {
	var notices []Notice
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/notifications/%d", pupil, kind), nil, &notices)
	return notices, err
}

// SchoolYear is one school year as the absence list names it.
type SchoolYear struct {
	ID        string `json:"municipalityYearId"`
	FromYear  int    `json:"fromYear"`
	ToYear    int    `json:"toYear"`
	IsCurrent bool   `json:"isCurrent"`
}

func (c *Client) AbsenceYears(ctx context.Context, pupil int) ([]SchoolYear, error) {
	var years []SchoolYear
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/absences/years", pupil), nil, &years)
	return years, err
}

// The terms of a school year. Zero asks for the whole year.
const (
	TermFall   = 1
	TermSpring = 2
)

// Absence is one absence. Type 1 is a whole day and anything else is hours.
// Status 1 is documented. Only whole days have been seen.
type Absence struct {
	ID         int       `json:"id"`
	Type       int       `json:"type"`
	Status     int       `json:"status"`
	Date       LocalTime `json:"date"`
	Comment    string    `json:"comment"`
	ModifiedBy string    `json:"modifiedBy"`
	DaysAbsent int       `json:"daysAbsent"`
}

func termQuery(term int) string {
	if term == 0 {
		return ""
	}
	return fmt.Sprintf("?term=%d", term)
}

func (c *Client) Absences(ctx context.Context, pupil int, year string, term int) ([]Absence, error) {
	var absences []Absence
	path := fmt.Sprintf("vfs/v1/pupils/%d/absences/%s%s", pupil, url.PathEscape(year), termQuery(term))
	err := c.do(ctx, http.MethodGet, path, nil, &absences)
	return absences, err
}

// AbsenceSummary is the absence of a term or a year, added up.
type AbsenceSummary struct {
	UndocumentedDays    int `json:"undocumentedDays"`
	UndocumentedMinutes int `json:"undocumentedMinutes"`
	DocumentedDays      int `json:"documentedDays"`
	DocumentedMinutes   int `json:"documentedMinutes"`
	TotalDays           int `json:"totalDays"`
	TotalMinutes        int `json:"totalMinutes"`
}

func (c *Client) AbsenceSummary(ctx context.Context, pupil int, year string, term int) (*AbsenceSummary, error) {
	var summary AbsenceSummary
	path := fmt.Sprintf("vfs/v1/pupils/%d/absences/%s/summary%s", pupil, url.PathEscape(year), termQuery(term))
	err := c.do(ctx, http.MethodGet, path, nil, &summary)
	return &summary, err
}

// Form is a form the school asks the guardian to answer.
type Form struct {
	ID               int       `json:"id"`
	Title            string    `json:"title"`
	Deadline         LocalTime `json:"deadline"`
	PublishedOn      LocalTime `json:"publishedOn"`
	IsAnswered       bool      `json:"isAnswered"`
	IsDeadlinePassed bool      `json:"isDeadlinePassed"`
	SchoolName       string    `json:"schoolName"`
}

func (c *Client) Forms(ctx context.Context, pupil int) ([]Form, error) {
	var forms []Form
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/digital-forms", pupil), nil, &forms)
	return forms, err
}

// FormDetail is one form with its alternatives and what was answered.
type FormDetail struct {
	ID                 int               `json:"id"`
	Title              string            `json:"title"`
	PublishText        string            `json:"publishText"`
	FurtherInformation string            `json:"furtherInformation"`
	PublishedOn        LocalTime         `json:"publishedOn"`
	Deadline           LocalTime         `json:"deadline"`
	Alternatives       []FormAlternative `json:"alternatives"`
	Answers            *struct {
		LastModifiedBy string    `json:"lastModifiedBy"`
		LastModifiedOn LocalTime `json:"lastModifiedOn"`
		Answers        []struct {
			AlternativeID int `json:"alternativeId"`
		} `json:"answers"`
	} `json:"answers"`
	IsAnswered bool `json:"isAnswered"`
	IsReadOnly bool `json:"isReadOnly"`
}

// FormAlternative is one choice of a form.
type FormAlternative struct {
	ID    int    `json:"id"`
	Order int    `json:"order"`
	Text  string `json:"text"`
}

func (c *Client) Form(ctx context.Context, pupil, form int) (*FormDetail, error) {
	var detail FormDetail
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/digital-forms/%d", pupil, form), nil, &detail)
	return &detail, err
}

// The values of Consent.Status.
const (
	ConsentNotAnswered = 0
	ConsentYes         = 1
	ConsentNo          = 2
)

// Consent is one thing the school asks the guardian to consent to.
type Consent struct {
	ID         int       `json:"id"`
	Title      string    `json:"title"`
	Status     int       `json:"status"`
	ModifiedOn LocalTime `json:"modifiedOn"`
	ModifiedBy string    `json:"modifiedBy"`
	Content    string    `json:"content"`
}

func (c *Client) Consents(ctx context.Context, pupil int) ([]Consent, error) {
	var answer struct {
		Consents []Consent `json:"consents"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/consents", pupil), nil, &answer)
	return answer.Consents, err
}

// Application is an application the guardian sent the school, such as one
// for leave. The list answers with the first six fields, one application
// with all of them.
type Application struct {
	RequestID       int       `json:"requestId"`
	CreatedDate     LocalTime `json:"createdDate"`
	AnsweredBy      string    `json:"answeredBy"`
	AnsweredTime    LocalTime `json:"answeredTime"`
	Status          int       `json:"status"`
	ApplicationType int       `json:"applicationType"`

	RequestedBy     string    `json:"requestedBy"`
	FromDate        LocalTime `json:"fromDate"`
	ToDate          LocalTime `json:"toDate"`
	CauseName       string    `json:"causeName"`
	ParentComment   string    `json:"parentComment"`
	EmployeeComment string    `json:"employeeComment"`
}

// ApplicationTypes and ApplicationStatuses name the numbers, as the web
// client does.
var (
	ApplicationTypes = map[int]string{
		1: "leave", 2: "school transport", 3: "reduced payment", 4: "SFO", 5: "school change",
		6: "exemption from grading", 7: "removal of absence from diploma",
		8: "special accommodation during exams", 9: "change of subject choice",
	}
	ApplicationStatuses = map[int]string{
		1: "waiting for reply", 2: "documentation requested", 3: "in progress", 4: "approved", 5: "processed",
		6: "cancelled", 7: "declined", 8: "processing automatically", 9: "approved automatically", 10: "rejected automatically",
	}
)

func (c *Client) Applications(ctx context.Context, pupil int) ([]Application, error) {
	var applications []Application
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/applications", pupil), nil, &applications)
	return applications, err
}

func (c *Client) Application(ctx context.Context, pupil, request int) (*Application, error) {
	var application Application
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/applications/%d", pupil, request), nil, &application)
	return &application, err
}

type named struct {
	FullName string `json:"fullName"`
}

// Groups is the class of a child and who teaches it.
type Groups struct {
	ContactTeacher *named `json:"contactTeacher"`
	ClassGroup     *struct {
		Name         string  `json:"name"`
		Level        string  `json:"level"`
		Responsibles []named `json:"responsibles"`
	} `json:"classGroup"`
	SubjectGroups []struct {
		Subject struct {
			Name string `json:"name"`
		} `json:"subject"`
		Name         string  `json:"name"`
		Responsibles []named `json:"responsibles"`
	} `json:"subjectGroups"`
}

func (c *Client) Groups(ctx context.Context, pupil int) (*Groups, error) {
	var groups Groups
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/groups", pupil), nil, &groups)
	return &groups, err
}

// School is how to reach a school.
type School struct {
	Name         string `json:"name"`
	PhoneNumber  string `json:"phoneNumber"`
	EmailAddress string `json:"emailAddress"`
	Street       string `json:"street"`
	Zip          string `json:"zip"`
	City         string `json:"city"`
	Homepage     string `json:"homepage"`
}

func (c *Client) School(ctx context.Context, school string) (*School, error) {
	var answer School
	err := c.do(ctx, http.MethodGet, "vfs/v1/schools/"+url.PathEscape(school), nil, &answer)
	return &answer, err
}

// Link is a web address the school has listed for guardians.
type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

func (c *Client) SchoolLinks(ctx context.Context, school string) ([]Link, error) {
	var links []Link
	err := c.do(ctx, http.MethodGet, "vfs/v1/schools/"+url.PathEscape(school)+"/links", nil, &links)
	return links, err
}

// SchoolDocument is a file the school shares with every guardian.
type SchoolDocument struct {
	DocumentID int    `json:"documentId"`
	Title      string `json:"title"`
}

func (c *Client) SchoolDocuments(ctx context.Context, school string) ([]SchoolDocument, error) {
	var documents []SchoolDocument
	err := c.do(ctx, http.MethodGet, "vfs/v1/schools/"+url.PathEscape(school)+"/documents", nil, &documents)
	return documents, err
}

func (c *Client) SchoolDocumentFile(ctx context.Context, school string, document int) ([]byte, error) {
	return c.fetch(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/schools/%s/documents/%d", url.PathEscape(school), document), nil)
}

// DocumentYear is one school year as the document list names it.
type DocumentYear struct {
	ID       string `json:"schoolYearId"`
	FromYear int    `json:"fromYear"`
	ToYear   int    `json:"toYear"`
}

func (c *Client) DocumentYears(ctx context.Context, pupil int) ([]DocumentYear, error) {
	var years []DocumentYear
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/documents/years", pupil), nil, &years)
	return years, err
}

// PupilDocument is a file about one child, such as a decision on leave.
type PupilDocument struct {
	ArchiveFileID string    `json:"archiveFileId"`
	Title         string    `json:"title"`
	CreatedBy     string    `json:"createdBy"`
	CreatedOn     LocalTime `json:"createdOn"`
	SchoolName    string    `json:"schoolName"`
}

func (c *Client) PupilDocuments(ctx context.Context, pupil int, year string) ([]PupilDocument, error) {
	var documents []PupilDocument
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/documents/years/%s", pupil, url.PathEscape(year)), nil, &documents)
	return documents, err
}

func (c *Client) PupilDocumentFile(ctx context.Context, pupil int, file string) ([]byte, error) {
	return c.fetch(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/pupils/%d/documents/%s", pupil, url.PathEscape(file)), nil)
}
