package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/ctltest"
	"github.com/langtind/ctlkit/keystore"
	"github.com/langtind/ctlkit/session"

	"github.com/langtind/skolectl/internal/flyt"
)

// call is one request skolectl sent to the fake service.
type call struct {
	Method string
	Path   string
	Query  string
	// Access, Refresh and Session are the three login cookies.
	Access  string
	Refresh string
	Session string
	// Form is the "request" field of a multipart form.
	Form string
	// Files lists the "attachments" of a multipart form, each as
	// "name type bytes".
	Files []string
}

// harness runs skolectl against a fake Visma Flyt Skole, with the login in memory.
type harness struct {
	t      *testing.T
	tokens *keystore.Memory
	calls  []call

	// answer is what the fake service says. The default serves one child
	// with a few threads.
	answer  func(c call) (int, string)
	lockDir string
}

var (
	testZone = time.FixedZone("CEST", 2*60*60)
	testTime = time.Date(2026, 10, 12, 15, 0, 0, 0, testZone)
)

// accessToken builds a token that runs out at the given time, in the form
// the service uses.
func accessToken(name string, expires time.Time) string {
	claims := fmt.Sprintf(`{"sub":%q,"exp":%d}`, name, expires.Unix())
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".s"
}

var renewedAccess = accessToken("renewed", testTime.Add(5*time.Minute))

// login stores a session. left is how long the access token has to live.
func (h *harness) login(left time.Duration) string {
	h.t.Helper()
	access := accessToken("old", testTime.Add(left))
	m := session.Manager{Store: h.tokens}
	err := m.Save(session.Session{AccessToken: access, AccessExpires: testTime.Add(left), RefreshToken: "refresh-1", Extra: map[string]string{flyt.ExtraSessionID: "session-1"}})
	if err != nil {
		h.t.Fatal(err)
	}
	return access
}

func (h *harness) stored() session.Session {
	h.t.Helper()
	m := session.Manager{Store: h.tokens}
	s, _ := m.Load()
	return s
}

// sent lists the requests as "METHOD path".
func (h *harness) sent() string {
	parts := make([]string, len(h.calls))
	for i, c := range h.calls {
		parts[i] = c.Method + " " + c.Path
	}
	return strings.Join(parts, ", ")
}

const pupil = "/vfs/v1/pupils/7"

var fixtures = map[string]string{
	"GET /v1/session":  `{"firstName":"Kari","lastName":"Nordmann","municipality":{"name":"Fjellvik skole","customerKey":"fjv"}}`,
	"GET /v2/children": `{"children":[{"firstName":"Ola","lastName":"Nordmann","school":{"pupilId":7,"schoolId":"sch-1","level":3,"organizationName":"Fjellvik skole"}},{"firstName":"Lille","lastName":"Nordmann"}]}`,

	"GET " + pupil + "/messages/message-info": `{"directMessagesUnreadCount":2,"groupMessagesUnreadCount":1}`,
	"GET " + pupil + "/messages/direct": `[
		{"idMessage":"m1","sentDate":"2026-10-01T08:00:00.000","title":"Gym","messageText":"Remember shoes","sender":"Per Hansen","employeeId":31,"unreadCount":0},
		{"idMessage":"m2","sentDate":"2026-10-09T14:30:00.000","title":"Trip","messageText":"Bring lunch","sender":"Anne Lie","employeeId":32,"unreadCount":2}]`,
	"GET /vfs/v2/pupils/7/messages/direct/32": `{"canReply":true,"messages":[
		{"idMessage":"m2","title":"Trip","messageText":"Bring lunch\n# not a heading","sentDate":"2026-10-09T14:30:00.000","readDate":null,"participantType":1,"attachments":[]},
		{"idMessage":"m0","title":"","messageText":"Is there a trip?","sentDate":"2026-10-08T10:00:00.000","readDate":"2026-10-08T11:00:00.000","participantType":2,"attachments":[]}]}`,
	"GET " + pupil + "/messages/group": `[
		{"conversationId":"c1","title":"Class 3A","concerningPupils":"Ola Nordmann","lastMessage":"Welcome back","createdOn":"2026-08-15T09:00:00.00","lastMessageDate":"2026-10-05T09:00:00.000","unreadMessageCount":1,"isReadonly":false}]`,
	"GET " + pupil + "/messages/group/c1": `{"conversationId":"c1","title":"Class 3A","isReadonly":false,"participants":["Anne Lie","Kari Nordmann"],"messages":[
		{"sentOn":"2026-10-05T09:00:00.00","sentBy":"Anne Lie","message":"Welcome back","sentByMe":false,"attachments":[{"attachmentId":"a1","fileName":"../plan.pdf","contentType":"application/pdf","contentLength":9}]}]}`,
	"GET " + pupil + "/messages/group/c1/attachment/a1":   "%PDF-1.4\n",
	"GET /vfs/v1/schools/sch-1/applicationTypes/1":        `{"name":"Leave","type":1,"hasRulesDocument":false,"template":{"applicationInfoText":"Pick the cause that fits.\n# not a heading","archiveText":"Kept for five years."}}`,
	"GET /vfs/v1/schools/sch-1/applicationTypes/1/causes": `[{"id":4,"name":"Family","isDefault":false,"onlyForLevelZero":false},{"id":5,"name":"Other","isDefault":true,"onlyForLevelZero":false}]`,
	"POST /vfs/v1/schools/sch-1/applications":             ``,
	"POST " + pupil + "/messages/direct/32/reply":         ``,
	"POST " + pupil + "/messages/group/c1/reply":          ``,
	"POST " + pupil + "/messages/direct/32/mark-as-read":  ``,
	"POST " + pupil + "/messages/group/c1/mark-as-read":   ``,
	"GET " + pupil + "/notifications/0":                   `[{"idMessage":"n1","sentDate":"2026-10-02T08:00:00.000","title":"Parent meeting","sender":"Fjellvik skole","messageText":"Tuesday at six","isUnread":true}]`,
	"GET " + pupil + "/notifications/1":                   `[{"idMessage":"n2","sentDate":"2026-09-01T08:00:00.000","title":"School start","sender":"Fjellvik skole","messageText":"Welcome","isUnread":false}]`,
	"GET " + pupil + "/absences/years":                    `[{"municipalityYearId":"y1","fromYear":2025,"toYear":2026,"isCurrent":false},{"municipalityYearId":"y2","fromYear":2026,"toYear":2027,"isCurrent":true}]`,
	"GET " + pupil + "/absences/y2":                       `[{"id":1,"type":1,"status":1,"date":"2026-09-03T00:00:00","comment":"Ill","modifiedBy":"Anne Lie","daysAbsent":1},{"id":2,"type":2,"status":0,"date":"2026-09-20T00:00:00","comment":"","modifiedBy":"Anne Lie","daysAbsent":0}]`,
	"GET " + pupil + "/absences/y2/summary":               `{"undocumentedDays":0,"undocumentedMinutes":45,"documentedDays":1,"documentedMinutes":0,"totalDays":1,"totalMinutes":45}`,
	"GET " + pupil + "/digital-forms":                     `[{"id":5,"title":"Swimming","deadline":"2026-10-20T00:00:00","publishedOn":"2026-10-01T00:00:00","isAnswered":false,"isDeadlinePassed":false},{"id":4,"title":"Photo day","deadline":"2026-09-01T00:00:00","publishedOn":"2026-08-20T00:00:00","isAnswered":true,"isDeadlinePassed":true}]`,
	"GET " + pupil + "/digital-forms/5":                   `{"id":5,"title":"Swimming","publishText":"Can Ola swim?","furtherInformation":"","publishedOn":"2026-10-01T00:00:00","deadline":"2026-10-20T00:00:00","alternatives":[{"id":52,"order":2,"text":"No"},{"id":51,"order":1,"text":"Yes"}],"answers":{"lastModifiedBy":"Kari Nordmann","lastModifiedOn":"2026-10-02T07:00:00.000","answers":[{"alternativeId":51}]},"isAnswered":true,"isReadOnly":false}`,
	"GET " + pupil + "/consents":                          `{"consents":[{"id":1,"title":"Photos","status":1,"modifiedOn":"2026-08-20T10:00:00.00","modifiedBy":"Kari Nordmann","content":"May we publish photos?"},{"id":2,"title":"Trips","status":0,"content":"May Ola join trips?"}],"canReply":true}`,
	"GET " + pupil + "/applications":                      `[{"requestId":9,"createdDate":"2026-09-10T12:00:00.000","answeredBy":"Per Hansen","answeredTime":"2026-09-11T08:00:00.000","status":4,"applicationType":1},{"requestId":10,"createdDate":"2026-10-10T12:00:00.000","status":1,"applicationType":1}]`,
	"GET " + pupil + "/applications/9":                    `{"requestId":9,"createdDate":"2026-09-10T12:00:00.000","answeredBy":"Per Hansen","answeredTime":"2026-09-11T08:00:00.000","status":4,"applicationType":1,"requestedBy":"Kari Nordmann","fromDate":"2026-09-20T00:00:00","toDate":"2026-09-22T00:00:00","causeName":"Family","parentComment":"A wedding","employeeComment":"Approved"}`,
	"GET " + pupil + "/groups":                            `{"contactTeacher":{"fullName":"Anne Lie"},"classGroup":{"name":"3A","level":"3","responsibles":[{"fullName":"Anne Lie"}]},"subjectGroups":[{"subject":{"name":"Maths"},"name":"3A Maths","responsibles":[{"fullName":"Per Hansen"}]}]}`,
	"GET /vfs/v1/schools/sch-1":                           `{"name":"Fjellvik skole","phoneNumber":"55501234","emailAddress":"post@fjellvik.invalid","street":"Skoleveien 1","zip":"0001","city":"Fjellvik","homepage":"https://fjellvik.invalid"}`,
	"GET /vfs/v1/schools/sch-1/links":                     `[{"title":"Calendar","url":"https://fjellvik.invalid/calendar"}]`,
	"GET /vfs/v1/schools/sch-1/documents":                 `[{"documentId":3,"title":"Rules"}]`,
	"GET /vfs/v1/schools/sch-1/documents/3":               "%PDF-1.4\n",
	"GET " + pupil + "/documents/years":                   `[{"schoolYearId":"dy1","fromYear":2025,"toYear":2026,"isCurrent":false}]`,
	"GET " + pupil + "/documents/years/dy1":               `[{"archiveFileId":"f1","documentTypeId":"t1","title":"Decision","createdBy":"Per Hansen","createdOn":"2026-03-01T10:00:00.000","schoolName":"Fjellvik skole"}]`,
	"GET " + pupil + "/documents/f1":                      "%PDF-1.4\n",
	"GET /vfs/v2/pupils/7/messages/direct/99":             `{"message":"Internal server error","errorCode":"e1"}`,
	"GET " + pupil + "/messages/group/c1/attachment/nope": ``,
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, tokens: &keystore.Memory{}, lockDir: t.TempDir()}
	h.login(4 * time.Minute)
	h.answer = func(c call) (int, string) {
		if c.Path == "/vfs/v2/pupils/7/messages/direct/99" {
			return http.StatusInternalServerError, fixtures["GET "+c.Path]
		}
		if answer, ok := fixtures[c.Method+" "+c.Path]; ok {
			return http.StatusOK, answer
		}
		return http.StatusNotFound, `{"message":"no such thing"}`
	}
	return h
}

// program returns skolectl wired to the fake service. It works without a
// server too, which is what the contract test needs.
func (h *harness) program(baseURL string, client *http.Client) *ctlkit.Program {
	app := &App{
		Client:  &flyt.Client{HTTP: client, BaseURL: baseURL},
		Session: &session.Manager{Store: h.tokens, LockPath: filepath.Join(h.lockDir, "renew.lock")},
		Now:     func() time.Time { return testTime },
		Zone:    testZone,
	}
	return app.program(ctlkit.Build{Version: "1.2.3"}, os.DirFS("../../skills/"+Name))
}

func cookie(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func (h *harness) run(stdin string, args ...string) (code int, stdout, stderr string) {
	h.t.Helper()
	h.calls = nil
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Access:  cookie(r, "CookieAccessToken"),
			Refresh: cookie(r, "CookieRefreshToken"),
			Session: cookie(r, "GwebId"),
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			c.Form = r.FormValue("request")
			for _, file := range r.MultipartForm.File["attachments"] {
				c.Files = append(c.Files, fmt.Sprintf("%s %s %d", file.Filename, file.Header.Get("Content-Type"), file.Size))
			}
		} else {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		if got := r.Header.Get("User-Agent"); got != Name+"/1.2.3 (+https://github.com/langtind/"+Name+")" {
			h.t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Requested-By"); got != "XMLHttpRequest" {
			h.t.Errorf("Requested-By = %q", got)
		}
		h.calls = append(h.calls, c)
		status, answer := h.answer(c)
		// As the service does: a request that carries the refresh token
		// is answered with a new access token.
		if status == http.StatusOK && c.Refresh != "" {
			http.SetCookie(w, &http.Cookie{Name: "CookieAccessToken", Value: renewedAccess})
			http.SetCookie(w, &http.Cookie{Name: "CookieRefreshToken", Value: c.Refresh})
		}
		w.WriteHeader(status)
		fmt.Fprint(w, answer)
	}))
	defer srv.Close()
	return ctltest.Run(h.program(srv.URL, srv.Client()), stdin, args...)
}

func TestContract(t *testing.T) {
	build := func() *ctlkit.Program { return newHarness(t).program("http://127.0.0.1:0", http.DefaultClient) }
	ctltest.Contract(t, build, "../../skills/"+Name+"/SKILL.md", "../../README.md")
}

func TestChildrenLeavesOutAChildWithoutASchool(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("", "children")
	want := "# Children\n\n- Ola Nordmann\n  School: Fjellvik skole\n  Level: 3\n  pupil id: 7\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if c := h.calls[0]; c.Session != "session-1" || c.Access == "" || c.Refresh != "" {
		t.Errorf("cookies = %+v", c)
	}
}

func TestThreadsAreMergedLatestFirst(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "threads", "--json", "--fields", "id,kind,employee,unread,lastMessageAt")
	var list struct {
		Items []map[string]any `json:"items"`
	}
	ctltest.OneJSON(t, stdout, &list)
	if code != 0 || len(list.Items) != 3 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	got := fmt.Sprintf("%v %v %v", list.Items[0]["id"], list.Items[1]["id"], list.Items[2]["id"])
	if got != "7.d.32 7.g.c1 7.d.31" || list.Items[0]["employee"] != "Anne Lie" || list.Items[0]["lastMessageAt"] != "2026-10-09T14:30:00+02:00" {
		t.Errorf("order = %s, first = %v", got, list.Items[0])
	}
}

func TestThreadsSaysWhenTheListIsCutOff(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "threads", "--limit", "1", "--json", "--fields", "id")
	var list struct {
		Items     []map[string]string `json:"items"`
		Truncated bool                `json:"truncated"`
	}
	ctltest.OneJSON(t, stdout, &list)
	if code != 0 || !list.Truncated || len(list.Items) != 1 || list.Items[0]["id"] != "7.d.32" {
		t.Errorf("exit = %d, list = %+v", code, list)
	}
	if _, stdout, _ = h.run("", "threads", "--limit", "1"); !strings.HasSuffix(stdout, "\nThere are more. Raise --limit to see them.\n") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestThreadsFilters(t *testing.T) {
	h := newHarness(t)
	if _, stdout, _ := h.run("", "threads", "--unread", "--kind", "direct"); strings.Count(stdout, "\n- ") != 1 || !strings.Contains(stdout, "id: 7.d.32") {
		t.Errorf("stdout = %q", stdout)
	}
	if h.sent() != "GET /v2/children, GET "+pupil+"/messages/direct" {
		t.Errorf("sent = %s", h.sent())
	}
	if code, _, stderr := h.run("", "threads", "--child", "8"); code != 3 || !strings.Contains(stderr, "no child with pupil id 8") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	h.answer = func(c call) (int, string) {
		if c.Path == "/v2/children" {
			return http.StatusOK, fixtures["GET /v2/children"]
		}
		return http.StatusOK, `[]`
	}
	_, stdout, _ := h.run("", "threads", "--json")
	if got := strings.Join(strings.Fields(stdout), ""); got != `{"items":[],"truncated":false}` {
		t.Errorf("stdout = %s", got)
	}
}

func TestThreadShowsADirectThreadOldestFirst(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("", "thread", "7.d.32")
	want := "# Thread 7.d.32\n\nid: 7.d.32\nKind: direct\nParticipants: Anne Lie\n" +
		"\n## 2026-10-08T10:00:00+02:00, me\n\n> Is there a trip?\n" +
		"\n## 2026-10-09T14:30:00+02:00, Anne Lie\n\nTitle: Trip\n\n> Bring lunch\n> # not a heading\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("exit = %d, stderr = %q, stdout = %q", code, stderr, stdout)
	}
	for _, c := range h.calls {
		if c.Method != http.MethodGet {
			t.Errorf("reading a thread sent %s %s", c.Method, c.Path)
		}
	}
}

func TestThreadShowsAGroupConversation(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "thread", "7.g.c1", "--json")
	var detail threadDetail
	ctltest.OneJSON(t, stdout, &detail)
	if code != 0 || detail.Title != "Class 3A" || !detail.CanReply || len(detail.Items) != 1 || detail.Items[0].From != "Anne Lie" || detail.Items[0].Attachments[0].ID != "a1" {
		t.Errorf("exit = %d, detail = %+v", code, detail)
	}
}

func TestAnUnknownThreadSaysWhereTheIdsAre(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "thread", "7.d.99", "--json")
	var doc ctltest.ErrorDocument
	ctltest.OneJSON(t, stdout, &doc)
	if code != 7 || doc.Error.Kind != "unavailable" || !strings.Contains(doc.Error.Hint, `Run "skolectl threads"`) {
		t.Errorf("exit = %d, error = %+v", code, doc.Error)
	}
	if code, _, stderr := h.run("", "thread", "nonsense"); code != 2 || !strings.Contains(stderr, `"nonsense" is not a thread id`) || len(h.calls) != 0 {
		t.Errorf("exit = %d, stderr = %q, calls = %+v", code, stderr, h.calls)
	}
}

func TestReplyNeedsAYes(t *testing.T) {
	h := newHarness(t)
	code, _, stderr := h.run("", "reply", "7.d.32", "Thanks")
	if code != 2 || !strings.Contains(stderr, "refusing without confirmation: Send this message to Anne Lie?") {
		t.Errorf("without --yes: exit = %d, stderr = %q", code, stderr)
	}
	for _, c := range h.calls {
		if c.Method != http.MethodGet {
			t.Errorf("without --yes, sent %s %s", c.Method, c.Path)
		}
	}

	code, stdout, _ := h.run("", "reply", "7.d.32", "Thanks", "--yes")
	last := h.calls[len(h.calls)-1]
	if code != 0 || stdout != "Sent to Anne Lie.\n" || last.Method != http.MethodPost || last.Path != pupil+"/messages/direct/32/reply" || last.Form != `{"text":"Thanks"}` {
		t.Errorf("with --yes: exit = %d, stdout = %q, last = %+v", code, stdout, last)
	}

	code, stdout, _ = h.run("", "reply", "7.g.c1", "See you", "--yes")
	last = h.calls[len(h.calls)-1]
	if code != 0 || stdout != "Sent to \"Class 3A\" (Anne Lie, Kari Nordmann).\n" || last.Path != pupil+"/messages/group/c1/reply" || last.Form != `{"text":"See you"}` {
		t.Errorf("group: exit = %d, stdout = %q, last = %+v", code, stdout, last)
	}
}

func TestADryRunReplySendsNothing(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "reply", "7.d.32", "Thanks", "--dry-run", "--json")
	var result replyResult
	ctltest.OneJSON(t, stdout, &result)
	if code != 0 || !result.DryRun || result.Sent || result.ThreadID != "7.d.32" || result.Request.Text != "Thanks" {
		t.Errorf("exit = %d, result = %+v", code, result)
	}
	for _, c := range h.calls {
		if c.Method != http.MethodGet {
			t.Errorf("a dry run sent %s %s", c.Method, c.Path)
		}
	}
	if code, _, stderr := h.run("", "reply", "7.d.32", "  ", "--yes"); code != 2 || !strings.Contains(stderr, "must not be empty") {
		t.Errorf("empty text: exit = %d, stderr = %q", code, stderr)
	}
}

func TestMarkReadNeedsAYes(t *testing.T) {
	h := newHarness(t)
	if code, _, stderr := h.run("", "mark-read", "7.d.32"); code != 2 || !strings.Contains(stderr, "Mark thread 7.d.32 as read?") || len(h.calls) != 0 {
		t.Errorf("without --yes: exit = %d, stderr = %q, calls = %+v", code, stderr, h.calls)
	}
	if code, stdout, _ := h.run("", "mark-read", "7.d.32", "--yes"); code != 0 || stdout != "Marked thread 7.d.32 as read.\n" || h.sent() != "POST "+pupil+"/messages/direct/32/mark-as-read" {
		t.Errorf("direct: exit = %d, stdout = %q, sent = %s", code, stdout, h.sent())
	}
	if code, _, _ := h.run("", "mark-read", "7.g.c1", "--yes"); code != 0 || h.sent() != "POST "+pupil+"/messages/group/c1/mark-as-read" {
		t.Errorf("group: exit = %d, sent = %s", code, h.sent())
	}
	if code, _, _ := h.run("", "mark-read", "7.g.c1", "--dry-run"); code != 0 || len(h.calls) != 0 {
		t.Errorf("dry run: exit = %d, calls = %+v", code, h.calls)
	}
}

func TestAttachmentIsSavedUnderItsOwnNameAndNeverOutsideTheDirectory(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())
	code, stdout, stderr := h.run("", "attachment", "7.g.c1", "a1")
	if code != 0 || stdout != "Saved plan.pdf (9 bytes, application/pdf).\n" {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if data, err := os.ReadFile("plan.pdf"); err != nil || string(data) != "%PDF-1.4\n" {
		t.Errorf("file = %q, err = %v", data, err)
	}
	if code, _, stderr := h.run("", "attachment", "7.g.c1", "a1"); code != 5 || !strings.Contains(stderr, "plan.pdf exists already") {
		t.Errorf("second time: exit = %d, stderr = %q", code, stderr)
	}
	if code, _, stderr := h.run("", "attachment", "7.g.c1", "nope"); code != 3 || !strings.Contains(stderr, "has no attachment nope") {
		t.Errorf("unknown: exit = %d, stderr = %q", code, stderr)
	}
	if code, _, _ := h.run("", "attachment", "7.d.32", "a1"); code != 2 {
		t.Errorf("direct: exit = %d", code)
	}
}

func TestOverviewCountsWhatWaits(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "overview", "--json")
	var result overview
	ctltest.OneJSON(t, stdout, &result)
	want := overviewItem{PupilID: 7, Child: "Ola Nordmann", UnreadDirect: 2, UnreadGroup: 1, UnreadNotices: 1, UnansweredForms: 1, UnansweredConsents: 1, WaitingApplications: 1}
	if code != 0 || len(result.Items) != 1 || result.Items[0] != want {
		t.Errorf("exit = %d, result = %+v", code, result)
	}
}

func TestNotices(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "notices", "--unread")
	want := "# Notices\n\n## Parent meeting (unread)\n\nSent: 2026-10-02T08:00:00+02:00\nFrom: Fjellvik skole\nChild: Ola Nordmann\nKind: notification\n\n> Tuesday at six\n"
	if code != 0 || stdout != want {
		t.Errorf("exit = %d, stdout = %q", code, stdout)
	}
	if _, stdout, _ = h.run("", "notices", "--kind", "announcement", "--json", "--fields", "id"); !strings.Contains(stdout, `"n2"`) || strings.Contains(stdout, `"n1"`) || h.sent() != "GET /v2/children, GET "+pupil+"/notifications/1" {
		t.Errorf("stdout = %s, sent = %s", stdout, h.sent())
	}
	if _, stdout, _ = h.run("", "notices", "--limit", "1", "--json"); !strings.Contains(stdout, `"truncated": true`) || !strings.Contains(stdout, `"n1"`) {
		t.Errorf("limit: stdout = %s", stdout)
	}
}

func TestAbsencesOfTheCurrentYear(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "absences")
	want := "# Absences\n\n## Ola Nordmann, 2026/2027, all year\n\nTotal: 1 days and 45 minutes\nDocumented: 1 days and 0 minutes\nNot documented: 0 days and 45 minutes\n\n" +
		"- 2026-09-20: hours, not documented\n  Changed by: Anne Lie\n- 2026-09-03: day, documented\n  Comment: Ill\n  Changed by: Anne Lie\n"
	if code != 0 || stdout != want {
		t.Errorf("exit = %d, stdout = %q", code, stdout)
	}
	if code, _, _ := h.run("", "absences", "--term", "spring"); code != 0 || h.calls[len(h.calls)-1].Query != "term=2" {
		t.Errorf("term: exit = %d, calls = %+v", code, h.calls)
	}
}

func TestFormsAndOneForm(t *testing.T) {
	h := newHarness(t)
	if code, stdout, _ := h.run("", "forms", "--unanswered"); code != 0 || stdout != "# Forms\n\n- Swimming\n  State: not answered\n  Deadline: 2026-10-20T00:00:00+02:00\n  Child: Ola Nordmann\n  id: 7.5\n" {
		t.Errorf("exit = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ := h.run("", "form", "7.5")
	want := "# Swimming\n\nid: 7.5\nDeadline: 2026-10-20T00:00:00+02:00\nAnswered: 2026-10-02T07:00:00+02:00 by Kari Nordmann\n\n> Can Ola swim?\n\nAlternatives:\n- [x] Yes\n- [ ] No\n"
	if code != 0 || stdout != want {
		t.Errorf("form: exit = %d, stdout = %q", code, stdout)
	}
	if code, _, stderr := h.run("", "form", "five"); code != 2 || !strings.Contains(stderr, `run "skolectl forms"`) {
		t.Errorf("bad id: exit = %d, stderr = %q", code, stderr)
	}
}

func TestConsents(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "consents", "--json", "--fields", "title,answer")
	if got := strings.Join(strings.Fields(stdout), ""); code != 0 || got != `{"items":[{"answer":"yes","title":"Photos"},{"answer":"notanswered","title":"Trips"}]}` {
		t.Errorf("exit = %d, stdout = %s", code, got)
	}
}

func TestApplicationsAndOneApplication(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "applications")
	want := "# Applications\n\n- leave: waiting for reply\n  Sent: 2026-10-10T12:00:00+02:00\n  Child: Ola Nordmann\n  id: 7.10\n" +
		"- leave: approved\n  Sent: 2026-09-10T12:00:00+02:00\n  Child: Ola Nordmann\n  Answered by: Per Hansen\n  id: 7.9\n"
	if code != 0 || stdout != want {
		t.Errorf("exit = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ = h.run("", "application", "7.9")
	want = "# Application for leave\n\nid: 7.9\nStatus: approved\nSent: 2026-09-10T12:00:00+02:00\nSent by: Kari Nordmann\nFrom: 2026-09-20\nTo: 2026-09-22\nCause: Family\nAnswered: 2026-09-11T08:00:00+02:00\nAnswered by: Per Hansen\n" +
		"\nThe guardian wrote:\n\n> A wedding\n\nThe school answered:\n\n> Approved\n"
	if code != 0 || stdout != want {
		t.Errorf("one: exit = %d, stdout = %q", code, stdout)
	}
}

func TestClassesAndSchools(t *testing.T) {
	h := newHarness(t)
	if code, stdout, _ := h.run("", "classes"); code != 0 || stdout != "# Classes\n\n## Ola Nordmann\n\nClass: 3A\nContact teacher: Anne Lie\nTeachers: Anne Lie\n\n- Maths: Per Hansen\n" {
		t.Errorf("classes: exit = %d, stdout = %q", code, stdout)
	}
	want := "# Schools\n\n## Fjellvik skole\n\nPhone: 55501234\nEmail: post@fjellvik.invalid\nAddress: Skoleveien 1, 0001 Fjellvik\nHomepage: https://fjellvik.invalid\n\nLinks:\n- Calendar: https://fjellvik.invalid/calendar\n"
	if code, stdout, _ := h.run("", "schools"); code != 0 || stdout != want {
		t.Errorf("schools: exit = %d, stdout = %q", code, stdout)
	}
}

func TestDocumentsAndOneDocument(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir())
	code, stdout, _ := h.run("", "documents", "--json", "--fields", "id,scope,title")
	if got := strings.Join(strings.Fields(stdout), ""); code != 0 || got != `{"items":[{"id":"p.7.f1","scope":"child","title":"Decision"},{"id":"s.sch-1.3","scope":"school","title":"Rules"}]}` {
		t.Errorf("exit = %d, stdout = %s", code, got)
	}
	if code, stdout, _ := h.run("", "document", "p.7.f1"); code != 0 || stdout != "Saved document-f1.pdf (9 bytes, application/pdf).\n" || h.sent() != "GET "+pupil+"/documents/f1" {
		t.Errorf("child: exit = %d, stdout = %q, sent = %s", code, stdout, h.sent())
	}
	if code, stdout, _ := h.run("", "document", "s.sch-1.3", "--output", "rules.pdf", "--json"); code != 0 || !strings.Contains(stdout, `"path": "rules.pdf"`) || h.sent() != "GET /vfs/v1/schools/sch-1/documents/3" {
		t.Errorf("school: exit = %d, stdout = %q, sent = %s", code, stdout, h.sent())
	}
	if code, _, _ := h.run("", "document", "x.1.2"); code != 2 {
		t.Errorf("bad id: exit = %d", code)
	}
}

func TestWithoutALoginNothingIsSent(t *testing.T) {
	h := newHarness(t)
	h.tokens.Secret = ""
	code, _, stderr := h.run("", "threads")
	if code != 4 || !strings.HasPrefix(stderr, "Error: not logged in\nHint: a person has to log in with ID-porten in a browser.") || len(h.calls) != 0 {
		t.Errorf("exit = %d, stderr = %q, calls = %+v", code, stderr, h.calls)
	}
}

// browserState is what a browser saves after a login, with a cookie from
// another site that must be left alone.
func browserState(access string) string {
	return fmt.Sprintf(`{"cookies":[
		{"name":"CookieAccessToken","value":%q,"domain":"api.foresatt.visma.no"},
		{"name":"CookieRefreshToken","value":"refresh-9","domain":"api.foresatt.visma.no"},
		{"name":"GwebId","value":"session-9","domain":"api.foresatt.visma.no"},
		{"name":"session","value":"other","domain":".connect.visma.com"}]}`, access)
}

func TestLoginTakesOverTheBrowsersLogin(t *testing.T) {
	h := newHarness(t)
	h.tokens.Secret = ""
	code, stdout, stderr := h.run(browserState(accessToken("browser", testTime.Add(-time.Hour))), "login", "--state-stdin")
	if code != 0 || stdout != "Logged in as Kari Nordmann at Fjellvik skole. The login is in the system keyring and renews itself.\n" {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if got := h.stored(); got.AccessToken != renewedAccess || got.RefreshToken != "refresh-9" || got.Extra[flyt.ExtraSessionID] != "session-9" || !got.AccessExpires.Equal(testTime.Add(5*time.Minute)) {
		t.Errorf("stored = %+v", got)
	}
	if c := h.calls[0]; h.sent() != "GET /v1/session, GET /v1/session" || c.Refresh != "refresh-9" || c.Session != "session-9" || h.calls[1].Refresh != "" {
		t.Errorf("sent = %s, calls = %+v", h.sent(), h.calls)
	}
}

func TestALoginTheServiceRefusesStoresNothing(t *testing.T) {
	h := newHarness(t)
	h.tokens.Secret = ""
	h.answer = func(call) (int, string) { return http.StatusUnauthorized, `` }
	code, _, stderr := h.run(browserState("a.b.c"), "login", "--state-stdin")
	if code != 4 || !strings.Contains(stderr, "refused the login in the browser state") || h.tokens.Secret != "" {
		t.Errorf("exit = %d, stderr = %q, stored = %q", code, stderr, h.tokens.Secret)
	}
	if code, _, stderr := h.run(`{"cookies":[]}`, "login", "--state-stdin"); code != 4 || !strings.Contains(stderr, "has no CookieAccessToken cookie") {
		t.Errorf("no cookies: exit = %d, stderr = %q", code, stderr)
	}
	if code, _, stderr := h.run("", "login"); code != 2 || !strings.Contains(stderr, `Run "skolectl help login"`) {
		t.Errorf("no flag: exit = %d, stderr = %q", code, stderr)
	}
}

func TestHelpLoginShowsTheSteps(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "help", "login")
	if code != 0 || !strings.Contains(stdout, "playwright-cli state-save state.json") || !strings.Contains(stdout, "skolectl login --state-stdin < state.json") {
		t.Errorf("exit = %d, stdout = %q", code, stdout)
	}
}

// A login stored before the session id moved to Extra cannot be renewed.
func TestAStoredLoginWithoutASessionIDSaysToLogInAgain(t *testing.T) {
	h := newHarness(t)
	m := session.Manager{Store: h.tokens}
	if err := m.Save(session.Session{AccessToken: accessToken("old", testTime.Add(-time.Hour)), AccessExpires: testTime.Add(-time.Hour), RefreshToken: "refresh-1 session-1"}); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := h.run("", "children")
	if code != 4 || !strings.HasPrefix(stderr, "Error: the login has run out: the stored login has no session id\nHint: a person has to log in with ID-porten") || h.sent() != "" {
		t.Errorf("exit = %d, stderr = %q, sent = %s", code, stderr, h.sent())
	}
}

func TestAnAccessTokenAboutToRunOutIsRenewedFirst(t *testing.T) {
	h := newHarness(t)
	h.login(10 * time.Second)
	code, _, stderr := h.run("", "children")
	if code != 0 || h.sent() != "GET /v1/session, GET /v2/children" || h.calls[0].Refresh != "refresh-1" || h.calls[0].Access != "" || h.calls[1].Access != renewedAccess || h.calls[1].Refresh != "" {
		t.Fatalf("exit = %d, stderr = %q, sent = %s, calls = %+v", code, stderr, h.sent(), h.calls)
	}
	if got := h.stored(); got.AccessToken != renewedAccess || got.RefreshToken != "refresh-1" || got.Extra[flyt.ExtraSessionID] != "session-1" {
		t.Errorf("stored = %+v", got)
	}
}

func TestATokenTheServiceRefusesIsRenewedAndTheRequestSentAgain(t *testing.T) {
	h := newHarness(t)
	old := h.login(4 * time.Minute)
	normal := h.answer
	h.answer = func(c call) (int, string) {
		if c.Access == old && c.Refresh == "" {
			return http.StatusUnauthorized, ``
		}
		return normal(c)
	}
	if code, _, stderr := h.run("", "children"); code != 0 || h.sent() != "GET /v2/children, GET /v1/session, GET /v2/children" {
		t.Errorf("exit = %d, stderr = %q, sent = %s", code, stderr, h.sent())
	}
}

func TestALoginThatCannotBeRenewedSaysToLogInAgain(t *testing.T) {
	h := newHarness(t)
	h.login(-time.Hour)
	h.answer = func(call) (int, string) { return http.StatusUnauthorized, `` }
	code, _, stderr := h.run("", "children")
	if code != 4 || !strings.HasPrefix(stderr, "Error: the login has run out: Visma Flyt Skole: HTTP 401\nHint: a person has to log in with ID-porten") || h.stored().RefreshToken != "refresh-1" {
		t.Errorf("exit = %d, stderr = %q, stored = %+v", code, stderr, h.stored())
	}
}

func TestStatusAndRefresh(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "status")
	if code != 0 || !strings.HasPrefix(stdout, "Logged in.\nAccess token until: 2026-10-12T15:04:00+02:00\nIt renews itself") || len(h.calls) != 0 {
		t.Errorf("status: exit = %d, sent = %s, stdout = %q", code, h.sent(), stdout)
	}

	code, stdout, _ = h.run("", "refresh", "--json")
	var status loginStatus
	ctltest.OneJSON(t, stdout, &status)
	if code != 0 || h.sent() != "GET /v1/session" || !status.LoggedIn || !status.Renewable || status.AccessExpires != "2026-10-12T13:05:00Z" {
		t.Errorf("refresh: exit = %d, sent = %s, status = %+v", code, h.sent(), status)
	}

	h.tokens.Secret = ""
	if _, stdout, _ = h.run("", "status"); !strings.HasPrefix(stdout, "Not logged in. A person has to log in") {
		t.Errorf("not logged in: %q", stdout)
	}
}

func TestLogoutRemovesTheLogin(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("", "logout"); code != 0 || h.tokens.Secret != "" {
		t.Errorf("exit = %d, stored = %q", code, h.tokens.Secret)
	}
}

func TestATimeWithoutAZoneIsReadAsOsloTime(t *testing.T) {
	var winter, summer flyt.LocalTime
	if err := json.Unmarshal([]byte(`"2026-01-15T08:00:00.5"`), &winter); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(`"2026-07-15T08:00:00"`), &summer)
	if winter.Format(time.RFC3339) != "2026-01-15T08:00:00+01:00" || summer.Format(time.RFC3339) != "2026-07-15T08:00:00+02:00" {
		t.Errorf("winter = %s, summer = %s", winter.Format(time.RFC3339), summer.Format(time.RFC3339))
	}
}

func TestLeaveOptionsListTheCausesAndTheLimits(t *testing.T) {
	h := newHarness(t)
	code, stdout, _ := h.run("", "leave-options", "--json")
	var list leaveOptionList
	ctltest.OneJSON(t, stdout, &list)
	if code != 0 || len(list.Items) != 1 {
		t.Fatalf("exit = %d, list = %+v", code, list)
	}
	got := list.Items[0]
	if got.PupilID != 7 || len(got.Causes) != 2 || got.Causes[1] != (leaveCause{ID: 5, Name: "Other", Default: true}) || got.MustAgreeToRules || got.Limits.MaxAttachments != 3 || got.Limits.MaxDays != 60 {
		t.Errorf("option = %+v", got)
	}
	_, stdout, _ = h.run("", "leave-options")
	if !strings.Contains(stdout, "- Other (default), cause id: 5\n") || !strings.Contains(stdout, "> # not a heading\n") {
		t.Errorf("stdout = %q", stdout)
	}
}

// leaveHarness serves a school that registers an application when it is
// sent one, as number 11.
func leaveHarness(t *testing.T, sendStatus int, registers bool) *harness {
	h := newHarness(t)
	sent := false
	normal := h.answer
	h.answer = func(c call) (int, string) {
		switch {
		case c.Method == http.MethodPost && c.Path == "/vfs/v1/schools/sch-1/applications":
			sent = registers
			return sendStatus, ``
		case sent && c.Path == pupil+"/applications":
			return http.StatusOK, `[{"requestId":11,"createdDate":"2026-10-12T15:00:01.000","status":1,"applicationType":1},{"requestId":9,"createdDate":"2026-09-10T12:00:00.000","status":4,"applicationType":1}]`
		case sent && c.Path == pupil+"/applications/11":
			return http.StatusOK, `{"requestId":11,"status":1,"applicationType":1,"fromDate":"2026-11-05T00:00:00","toDate":"2026-11-05T00:00:00"}`
		case c.Path == pupil+"/applications/10":
			return http.StatusOK, `{"requestId":10,"status":1,"applicationType":1,"fromDate":"2026-10-20T00:00:00","toDate":"2026-10-21T00:00:00"}`
		}
		return normal(c)
	}
	return h
}

func posts(h *harness) int {
	count := 0
	for _, c := range h.calls {
		if c.Method == http.MethodPost {
			count++
		}
	}
	return count
}

func letter(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplyLeaveShowsTheWholeApplicationOnADryRunAndSendsNothing(t *testing.T) {
	h := leaveHarness(t, http.StatusOK, true)
	pdf := letter(t, "choir.pdf", []byte("%PDF-1.4\nletter"))
	code, stdout, stderr := h.run("", "apply-leave", "7", "--from", "2026-11-05", "--reason", "Choir trip\n# not a heading", "--attach", pdf, "--dry-run")
	want := "Dry run. This application for leave would be sent. Nothing was sent.\n\n" +
		"Child: Ola Nordmann\nSchool: Fjellvik skole\nCause: Other\nFrom: 2026-11-05\nTo: 2026-11-05\nDays: 1\n\n" +
		"Reason:\n\n> Choir trip\n> # not a heading\n\nFiles:\n\n- choir.pdf (15 bytes, application/pdf)\n"
	if code != 0 || stdout != want || posts(h) != 0 {
		t.Errorf("exit = %d, posts = %d, stderr = %q, stdout = %q", code, posts(h), stderr, stdout)
	}
}

func TestApplyLeaveNeedsAYesThenSendsAndLooksTheApplicationUp(t *testing.T) {
	h := leaveHarness(t, http.StatusOK, true)
	pdf := letter(t, "choir.pdf", []byte("%PDF-1.4\nletter"))
	args := []string{"apply-leave", "7", "--from", "2026-11-05", "--cause", "4", "--reason", "Choir trip", "--attach", pdf}
	code, _, stderr := h.run("", args...)
	if code != 2 || !strings.Contains(stderr, "refusing without confirmation: Send this application for leave to Fjellvik skole?") || !strings.Contains(stderr, "Cause: Family") || posts(h) != 0 {
		t.Errorf("without --yes: exit = %d, posts = %d, stderr = %q", code, posts(h), stderr)
	}

	code, stdout, stderr := h.run("", append(args, "--yes", "--json")...)
	var result leaveResult
	ctltest.OneJSON(t, stdout, &result)
	if code != 0 || !result.Sent || !result.Verified || result.ApplicationID != "7.11" || result.DryRun {
		t.Fatalf("exit = %d, result = %+v, stderr = %q", code, result, stderr)
	}
	var sent call
	for _, c := range h.calls {
		if c.Method == http.MethodPost {
			sent = c
		}
	}
	wantForm := `{"applicationType":1,"comment":"Choir trip","applicationTypeCauseId":4,"fromDate":"2026-11-05T00:00:00","toDate":"2026-11-05T00:00:00","hasReadRules":null,"pupilInYearIds":[7]}`
	if posts(h) != 1 || sent.Path != "/vfs/v1/schools/sch-1/applications" || sent.Form != wantForm || fmt.Sprint(sent.Files) != "[choir.pdf application/pdf 15]" {
		t.Errorf("posts = %d, sent = %+v", posts(h), sent)
	}
}

func TestApplyLeaveRefusesASecondApplicationForTheSameDays(t *testing.T) {
	h := leaveHarness(t, http.StatusOK, true)
	code, _, stderr := h.run("", "apply-leave", "7", "--from", "2026-10-20", "--to", "2026-10-21", "--reason", "Trip", "--yes")
	if code != 5 || !strings.Contains(stderr, "already has an application for leave from 2026-10-20 to 2026-10-21: 7.10") || posts(h) != 0 {
		t.Errorf("exit = %d, posts = %d, stderr = %q", code, posts(h), stderr)
	}
}

func TestApplyLeaveSettlesASendThatGotNoClearAnswer(t *testing.T) {
	// The service failed, but it has the application: that is a success.
	h := leaveHarness(t, http.StatusBadGateway, true)
	code, stdout, stderr := h.run("", "apply-leave", "7", "--from", "2026-11-05", "--reason", "Choir trip", "--yes")
	if code != 0 || !strings.HasPrefix(stdout, "Sent, and the school has it as application 7.11.\n") || posts(h) != 1 {
		t.Errorf("registered: exit = %d, posts = %d, stdout = %q, stderr = %q", code, posts(h), stdout, stderr)
	}

	// The service failed and has nothing: say so, and do not try again.
	h = leaveHarness(t, http.StatusBadGateway, false)
	code, stdout, stderr = h.run("", "apply-leave", "7", "--from", "2026-11-05", "--reason", "Choir trip", "--yes")
	if code != 7 || stdout != "" || !strings.Contains(stderr, "it is not known whether the application was sent") || !strings.Contains(stderr, "do not send it again before you have looked") || posts(h) != 1 {
		t.Errorf("not registered: exit = %d, posts = %d, stdout = %q, stderr = %q", code, posts(h), stdout, stderr)
	}

	// A clear no is passed on as it is, and nothing is looked up.
	h = leaveHarness(t, http.StatusBadRequest, false)
	code, _, stderr = h.run("", "apply-leave", "7", "--from", "2026-11-05", "--reason", "Choir trip", "--yes")
	if code == 0 || strings.Contains(stderr, "not known") || h.calls[len(h.calls)-1].Method != http.MethodPost {
		t.Errorf("refused: exit = %d, stderr = %q, sent = %s", code, stderr, h.sent())
	}
}

func TestApplyLeaveChecksTheApplicationBeforeItCallsTheService(t *testing.T) {
	h := leaveHarness(t, http.StatusOK, true)
	big := letter(t, "big.pdf", append([]byte("%PDF-1.4\n"), make([]byte, 5*1024*1024)...))
	text := letter(t, "note.txt", []byte("just text"))
	pdf := letter(t, "ok.pdf", []byte("%PDF-1.4\n"))
	for name, test := range map[string]struct {
		args []string
		want string
	}{
		"no from":      {[]string{"--reason", "Trip"}, "--from is required"},
		"not a date":   {[]string{"--from", "5 November", "--reason", "Trip"}, "is not a date"},
		"in the past":  {[]string{"--from", "2026-10-11", "--reason", "Trip"}, "is in the past"},
		"to first":     {[]string{"--from", "2026-11-05", "--to", "2026-11-04", "--reason", "Trip"}, "is before --from"},
		"too long":     {[]string{"--from", "2026-11-05", "--to", "2027-01-05", "--reason", "Trip"}, "longer than the 60 days"},
		"no reason":    {[]string{"--from", "2026-11-05", "--reason", "  "}, "--reason is required"},
		"long reason":  {[]string{"--from", "2026-11-05", "--reason", strings.Repeat("å", 2001)}, "2001 characters"},
		"big file":     {[]string{"--from", "2026-11-05", "--reason", "Trip", "--attach", big}, "at most 5242880 bytes"},
		"wrong type":   {[]string{"--from", "2026-11-05", "--reason", "Trip", "--attach", text}, "has to be a PDF, a PNG or a JPEG"},
		"missing file": {[]string{"--from", "2026-11-05", "--reason", "Trip", "--attach", "/no/such.pdf"}, "cannot read the file"},
		"four files":   {[]string{"--from", "2026-11-05", "--reason", "Trip", "--attach", pdf + "," + letter(t, "b.pdf", nil) + "," + letter(t, "c.pdf", nil) + "," + letter(t, "d.pdf", nil)}, "at most 3 files"},
	} {
		code, _, stderr := h.run("", append([]string{"apply-leave", "7", "--yes"}, test.args...)...)
		if code != 2 || !strings.Contains(stderr, test.want) || len(h.calls) != 0 {
			t.Errorf("%s: exit = %d, calls = %d, stderr = %q", name, code, len(h.calls), stderr)
		}
	}
	if code, _, stderr := h.run("", "apply-leave", "7", "--from", "2026-11-05", "--reason", "Trip", "--cause", "99", "--yes"); code != 2 || !strings.Contains(stderr, "no cause of leave with id 99") || posts(h) != 0 {
		t.Errorf("unknown cause: exit = %d, stderr = %q", code, stderr)
	}
	if code, _, stderr := h.run("", "apply-leave", "8", "--from", "2026-11-05", "--reason", "Trip", "--yes"); code != 3 || !strings.Contains(stderr, "no child with pupil id 8") || posts(h) != 0 {
		t.Errorf("unknown child: exit = %d, stderr = %q", code, stderr)
	}
}

func TestApplyLeaveAsksToConfirmTheRulesWhereTheSchoolHasSome(t *testing.T) {
	h := leaveHarness(t, http.StatusOK, true)
	normal := h.answer
	h.answer = func(c call) (int, string) {
		if c.Path == "/vfs/v1/schools/sch-1/applicationTypes/1" {
			return http.StatusOK, `{"name":"Leave","type":1,"hasRulesDocument":true,"template":{}}`
		}
		return normal(c)
	}
	args := []string{"apply-leave", "7", "--from", "2026-11-05", "--reason", "Trip", "--yes"}
	if code, _, stderr := h.run("", args...); code != 2 || !strings.Contains(stderr, "has rules for leave") || posts(h) != 0 {
		t.Errorf("without --agree-rules: exit = %d, stderr = %q", code, stderr)
	}
	code, stdout, _ := h.run("", append(args, "--agree-rules")...)
	sent := ""
	for _, c := range h.calls {
		if c.Method == http.MethodPost {
			sent = c.Form
		}
	}
	if code != 0 || !strings.Contains(sent, `"hasReadRules":true`) || !strings.Contains(stdout, "confirms having read the school's rules") {
		t.Errorf("with --agree-rules: exit = %d, sent = %q, stdout = %q", code, sent, stdout)
	}
}
