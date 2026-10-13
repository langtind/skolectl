package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/httpx"

	"github.com/langtind/skolectl/internal/flyt"
)

// leaveCause is one cause a school takes for leave.
type leaveCause struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

// leaveLimits is what an application has to keep within.
type leaveLimits struct {
	MaxDays            int      `json:"maxDays"`
	MaxReasonLength    int      `json:"maxReasonLength"`
	MaxAttachments     int      `json:"maxAttachments"`
	MaxAttachmentBytes int      `json:"maxAttachmentBytes"`
	AttachmentTypes    []string `json:"attachmentTypes"`
}

var limits = leaveLimits{
	MaxDays: flyt.MaxLeaveDays, MaxReasonLength: flyt.MaxReasonLength, MaxAttachments: flyt.MaxAttachments,
	MaxAttachmentBytes: flyt.MaxAttachmentBytes, AttachmentTypes: flyt.AttachmentTypes,
}

// leaveOption is what one child's school asks of an application for leave.
type leaveOption struct {
	PupilID int          `json:"pupilId"`
	Child   string       `json:"child"`
	School  string       `json:"school"`
	Causes  []leaveCause `json:"causes"`
	// MustAgreeToRules says that the school has rules the guardian has to
	// confirm having read, with --agree-rules.
	MustAgreeToRules bool        `json:"mustAgreeToRules"`
	Required         []string    `json:"required"`
	Limits           leaveLimits `json:"limits"`
	// CauseInfo and ArchiveInfo are the school's own texts.
	CauseInfo   string `json:"causeInfo,omitempty"`
	ArchiveInfo string `json:"archiveInfo,omitempty"`
}

type leaveOptionList struct {
	Items []leaveOption `json:"items"`
}

// leaveOption reads what a child's school asks of an application for leave.
func (a *App) leaveOption(ctx context.Context, kid child) (leaveOption, error) {
	info, err := a.Client.ApplicationTypeInfo(ctx, kid.SchoolID, flyt.LeaveType)
	if err != nil {
		return leaveOption{}, err
	}
	causes, err := a.Client.ApplicationCauses(ctx, kid.SchoolID, flyt.LeaveType)
	if err != nil {
		return leaveOption{}, err
	}
	option := leaveOption{
		PupilID: kid.PupilID, Child: kid.Name, School: kid.School, Causes: []leaveCause{},
		MustAgreeToRules: info.HasRulesDocument, Required: []string{"from", "cause", "reason"}, Limits: limits,
		CauseInfo: info.Template.ApplicationInfoText, ArchiveInfo: info.Template.ArchiveText,
	}
	if info.HasRulesDocument {
		option.Required = append(option.Required, "agree-rules")
	}
	for _, cause := range causes {
		option.Causes = append(option.Causes, leaveCause{ID: cause.ID, Name: cause.Name, Default: cause.IsDefault})
	}
	return option, nil
}

func (a *App) leaveOptions(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.pick(ctx, c)
	if err != nil {
		return err
	}
	list := leaveOptionList{Items: []leaveOption{}}
	for _, kid := range children {
		option, err := a.leaveOption(ctx, kid)
		if err != nil {
			return err
		}
		list.Items = append(list.Items, option)
	}
	return c.Print(list, func(w io.Writer) { renderLeaveOptions(w, list) })
}

// leaveFile is one file that goes with an application.
type leaveFile struct {
	Name        string `json:"name"`
	Bytes       int    `json:"bytes"`
	ContentType string `json:"contentType"`
}

// leaveResult is what apply-leave prints.
type leaveResult struct {
	DryRun bool `json:"dryRun"`
	Sent   bool `json:"sent"`
	// Verified says that the application was found in the list after it
	// was sent. ApplicationID is its id there.
	Verified      bool   `json:"verified"`
	ApplicationID string `json:"applicationId,omitempty"`

	PupilID     int         `json:"pupilId"`
	Child       string      `json:"child"`
	School      string      `json:"school"`
	Cause       string      `json:"cause"`
	From        string      `json:"from"`
	To          string      `json:"to"`
	Days        int         `json:"days"`
	Reason      string      `json:"reason"`
	Attachments []leaveFile `json:"attachments"`
	// Request is the JSON that goes in the form field "request".
	Request flyt.LeaveRequest `json:"request"`
}

func parseDay(flag, text string) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", text, flyt.Oslo)
	if err != nil {
		return time.Time{}, ctlkit.Usagef("--%s %q is not a date", flag, text).WithHint("write it as yyyy-mm-dd.")
	}
	return day, nil
}

// readAttachments reads the files and checks them against what the portal
// takes.
func readAttachments(paths []string) ([]flyt.FormFile, error) {
	if len(paths) > flyt.MaxAttachments {
		return nil, ctlkit.Usagef("an application takes at most %d files, and %d were given", flyt.MaxAttachments, len(paths))
	}
	files := []flyt.FormFile{}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, ctlkit.Usagef("cannot read the file %s: %v", path, err)
		}
		if info.IsDir() {
			return nil, ctlkit.Usagef("%s is a directory", path)
		}
		if info.Size() > flyt.MaxAttachmentBytes {
			return nil, ctlkit.Usagef("%s is %d bytes, and a file can be at most %d bytes (5 MB)", path, info.Size(), flyt.MaxAttachmentBytes)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, ctlkit.Usagef("cannot read the file %s: %v", path, err)
		}
		contentType := http.DetectContentType(data)
		if !slices.Contains(flyt.AttachmentTypes, contentType) {
			return nil, ctlkit.Usagef("%s holds %s, and a file has to be a PDF, a PNG or a JPEG", path, contentType)
		}
		files = append(files, flyt.FormFile{Name: filepath.Base(path), ContentType: contentType, Data: data})
	}
	return files, nil
}

// sameLeave finds an application for leave with these days among the
// latest ones of a pupil. skip names the ids to leave out. One that was
// cancelled or declined does not count.
func (a *App) sameLeave(ctx context.Context, pupil int, from, to time.Time, skip map[int]bool) (int, map[int]bool, error) {
	all, err := a.Client.Applications(ctx, pupil)
	if err != nil {
		return 0, nil, err
	}
	slices.SortStableFunc(all, func(x, y flyt.Application) int { return y.CreatedDate.Compare(x.CreatedDate.Time) })
	seen := map[int]bool{}
	found := 0
	checked := 0
	for _, entry := range all {
		seen[entry.RequestID] = true
		closed := entry.Status == 6 || entry.Status == 7 || entry.Status == 10
		if found != 0 || skip[entry.RequestID] || entry.ApplicationType != flyt.LeaveType || closed || checked >= sameLeaveChecks {
			continue
		}
		checked++
		detail, err := a.Client.Application(ctx, pupil, entry.RequestID)
		if err != nil {
			return 0, nil, err
		}
		if date(detail.FromDate) == from.Format("2006-01-02") && date(detail.ToDate) == to.Format("2006-01-02") {
			found = entry.RequestID
		}
	}
	return found, seen, nil
}

// sameLeaveChecks is how many of the latest applications are read to look
// for one with the same days. Each is a request.
const sameLeaveChecks = 10

func (a *App) applyLeave(ctx context.Context, c *ctlkit.Call) error {
	pupil, err := strconv.Atoi(c.Arg(0))
	if err != nil {
		return ctlkit.Usagef("%q is not a pupil id", c.Arg(0)).WithHint(`run "%s children" to see the ids.`, Name)
	}
	if !c.Given("from") {
		return ctlkit.Usagef("--from is required: the first day of the leave")
	}
	from, err := parseDay("from", c.String("from"))
	if err != nil {
		return err
	}
	to := from
	if c.Given("to") {
		if to, err = parseDay("to", c.String("to")); err != nil {
			return err
		}
	}
	today := a.Now().In(flyt.Oslo)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, flyt.Oslo)
	switch {
	case from.Before(today):
		return ctlkit.Usagef("--from %s is in the past: leave is applied for from today or later", c.String("from"))
	case to.Before(from):
		return ctlkit.Usagef("--to %s is before --from %s", c.String("to"), c.String("from"))
	case to.After(from.AddDate(0, 0, flyt.MaxLeaveDays)):
		return ctlkit.Usagef("the leave is longer than the %d days one application can cover", flyt.MaxLeaveDays)
	}
	reason := strings.TrimSpace(c.String("reason"))
	if reason == "" {
		return ctlkit.Usagef("--reason is required: why the child needs leave")
	}
	if length := utf8.RuneCountInString(reason); length > flyt.MaxReasonLength {
		return ctlkit.Usagef("the reason is %d characters, and it can be at most %d", length, flyt.MaxReasonLength)
	}
	files, err := readAttachments(c.List("attach"))
	if err != nil {
		return err
	}

	if err := a.authorize(ctx); err != nil {
		return err
	}
	children, err := a.schoolChildren(ctx)
	if err != nil {
		return err
	}
	at := slices.IndexFunc(children, func(k child) bool { return k.PupilID == pupil })
	if at < 0 {
		return ctlkit.NotFoundf("no child with pupil id %d", pupil).WithHint(`run "%s children" to see the ids.`, Name)
	}
	kid := children[at]
	option, err := a.leaveOption(ctx, kid)
	if err != nil {
		return err
	}
	options := fmt.Sprintf(`run "%s leave-options --child %d" to see the causes.`, Name, pupil)
	var cause leaveCause
	switch {
	case c.Given("cause"):
		at := slices.IndexFunc(option.Causes, func(k leaveCause) bool { return k.ID == c.Int("cause") })
		if at < 0 {
			return ctlkit.Usagef("%s has no cause of leave with id %d", kid.School, c.Int("cause")).WithHint("%s", options)
		}
		cause = option.Causes[at]
	default:
		at := slices.IndexFunc(option.Causes, func(k leaveCause) bool { return k.Default })
		if at < 0 {
			return ctlkit.Usagef("--cause is required: %s has no default cause of leave", kid.School).WithHint("%s", options)
		}
		cause = option.Causes[at]
	}
	var agreed *bool
	if option.MustAgreeToRules {
		if !c.Bool("agree-rules") {
			return ctlkit.Usagef("%s has rules for leave that the guardian has to confirm having read", kid.School).
				WithHint("the rules are in the portal, under applications. Pass --agree-rules when the user has read them and says so.")
		}
		yes := true
		agreed = &yes
	}

	result := leaveResult{
		PupilID: pupil, Child: kid.Name, School: kid.School, Cause: cause.Name,
		From: from.Format("2006-01-02"), To: to.Format("2006-01-02"), Days: int(to.Sub(from).Hours()/24+0.5) + 1,
		Reason: reason, Attachments: []leaveFile{},
		Request: flyt.LeaveRequest{
			ApplicationType: flyt.LeaveType, Comment: reason, CauseID: cause.ID,
			FromDate: flyt.LeaveDate(from), ToDate: flyt.LeaveDate(to), HasReadRules: agreed, PupilIDs: []int{pupil},
		},
	}
	for _, file := range files {
		result.Attachments = append(result.Attachments, leaveFile{Name: file.Name, Bytes: len(file.Data), ContentType: file.ContentType})
	}

	// An application with the same days is refused, so that a second run
	// after a failure cannot send the application twice.
	same, before, err := a.sameLeave(ctx, pupil, from, to, nil)
	if err != nil {
		return err
	}
	if same != 0 {
		return ctlkit.Errorf(ctlkit.KindConflict, "%s already has an application for leave from %s to %s: %d.%d", kid.Name, result.From, result.To, pupil, same).
			WithHint(`run "%s application %d.%d" to read it. Nothing was sent.`, Name, pupil, same)
	}
	if c.DryRun() {
		result.DryRun = true
		return c.Print(result, func(w io.Writer) { renderLeave(w, result) })
	}
	preview := &strings.Builder{}
	renderLeavePreview(preview, result)
	if err := c.Confirm(fmt.Sprintf("Send this application for leave to %s?\n\n%s", kid.School, preview)); err != nil {
		return err
	}

	sendErr := a.Client.CreateApplication(ctx, kid.SchoolID, result.Request, files)
	if sendErr != nil && !unclear(sendErr) {
		return sendErr
	}
	// The answer to the request does not name the application, so it is
	// looked up. This also settles a request that got no answer.
	registered, _, lookErr := a.sameLeave(ctx, pupil, from, to, before)
	again := fmt.Sprintf(`do not send it again before you have looked: run "%s applications --child %d" in a minute. Sending again may give the school two applications.`, Name, pupil)
	switch {
	case sendErr != nil && registered == 0:
		return ctlkit.Errorf(ctlkit.KindOf(sendErr), "it is not known whether the application was sent: %w", sendErr).WithHint("%s", again)
	case lookErr != nil:
		result.Sent = true
		c.Note("The application was sent, but looking it up failed: %v. Run \"%s applications --child %d\" to see it.", lookErr, Name, pupil)
	case registered == 0:
		result.Sent = true
		c.Note("The application was sent, but it is not in the list yet. Run \"%s applications --child %d\" to see it.", Name, pupil)
	default:
		result.Sent, result.Verified = true, true
		result.ApplicationID = fmt.Sprintf("%d.%d", pupil, registered)
	}
	return c.Print(result, func(w io.Writer) { renderLeave(w, result) })
}

// unclear says that a failed request may still have reached the service:
// it got no answer, or the service failed while it handled it.
func unclear(err error) bool {
	status := httpx.Status(err)
	return status == 0 || status >= http.StatusInternalServerError
}

func renderLeaveOptions(w io.Writer, list leaveOptionList) {
	fmt.Fprint(w, "# Leave: what an application takes\n")
	for _, option := range list.Items {
		fmt.Fprintf(w, "\n## %s\n\n", oneLine(option.Child))
		fmt.Fprintf(w, "School: %s\npupil id: %d\n\nCauses:\n\n", oneLine(option.School), option.PupilID)
		for _, cause := range option.Causes {
			mark := ""
			if cause.Default {
				mark = " (default)"
			}
			fmt.Fprintf(w, "- %s%s, cause id: %d\n", oneLine(cause.Name), mark, cause.ID)
		}
		if len(option.Causes) == 0 {
			fmt.Fprintln(w, "- none: the school takes no applications for leave here")
		}
		fmt.Fprintf(w, "\nRequired: %s\n", strings.Join(option.Required, ", "))
		fmt.Fprintf(w, "Limits: at most %d days after the first day, a reason of at most %d characters, at most %d files of at most %d MB each (PDF, PNG or JPEG)\n",
			option.Limits.MaxDays, option.Limits.MaxReasonLength, option.Limits.MaxAttachments, option.Limits.MaxAttachmentBytes/1024/1024)
		if option.CauseInfo != "" {
			fmt.Fprintf(w, "\nThe school says about the causes:\n\n%s\n", ctlkit.Quote(option.CauseInfo))
		}
		if option.ArchiveInfo != "" {
			fmt.Fprintf(w, "\nThe school says about how it keeps the application:\n\n%s\n", ctlkit.Quote(option.ArchiveInfo))
		}
	}
}

// renderLeavePreview writes the whole application, as it is shown before
// it is sent.
func renderLeavePreview(w io.Writer, result leaveResult) {
	fmt.Fprintf(w, "Child: %s\nSchool: %s\nCause: %s\nFrom: %s\nTo: %s\nDays: %d\n",
		oneLine(result.Child), oneLine(result.School), oneLine(result.Cause), result.From, result.To, result.Days)
	if result.Request.HasReadRules != nil {
		fmt.Fprintln(w, "The guardian confirms having read the school's rules for leave.")
	}
	fmt.Fprintf(w, "\nReason:\n\n%s\n\nFiles:\n\n", ctlkit.Quote(result.Reason))
	for _, file := range result.Attachments {
		fmt.Fprintf(w, "- %s (%d bytes, %s)\n", file.Name, file.Bytes, file.ContentType)
	}
	if len(result.Attachments) == 0 {
		fmt.Fprintln(w, "- none")
	}
}

func renderLeave(w io.Writer, result leaveResult) {
	switch {
	case result.DryRun:
		fmt.Fprint(w, "Dry run. This application for leave would be sent. Nothing was sent.\n\n")
	case result.Verified:
		fmt.Fprintf(w, "Sent, and the school has it as application %s.\n\n", result.ApplicationID)
	default:
		fmt.Fprint(w, "Sent.\n\n")
	}
	renderLeavePreview(w, result)
}
