package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/langtind/ctlkit"
)

// Output is Markdown, which reads well both in a terminal and when an agent
// relays it into a chat. What the service returned is printed on one line,
// so that a line break in a name cannot pass for structure.

func renderChildren(w io.Writer, list childList) {
	fmt.Fprintln(w, "# Children")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo children at a school.")
		return
	}
	fmt.Fprintln(w)
	for _, kid := range list.Items {
		fmt.Fprintf(w, "- %s\n", oneLine(kid.Name))
		field(w, "School", kid.School)
		if kid.Level > 0 {
			fmt.Fprintf(w, "  Level: %d\n", kid.Level)
		}
		fmt.Fprintf(w, "  pupil id: %d\n", kid.PupilID)
	}
}

func renderThreads(w io.Writer, list threadList) {
	fmt.Fprintln(w, "# Threads")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo threads.")
		return
	}
	fmt.Fprintln(w)
	for _, t := range list.Items {
		title := oneLine(t.Title)
		if title == "" {
			title = "(no title)"
		}
		fmt.Fprintf(w, "- %s\n", title)
		if t.Unread > 0 {
			fmt.Fprintf(w, "  Unread: %d\n", t.Unread)
		}
		field(w, "With", t.Employee)
		field(w, "Child", t.Child)
		field(w, "Latest", t.LastMessageAt)
		field(w, "Preview", t.Preview)
		fmt.Fprintf(w, "  Kind: %s\n", t.Kind)
		field(w, "id", t.ID)
	}
	if list.Truncated {
		fmt.Fprintln(w, "\nThere are more. Raise --limit to see them.")
	}
}

func renderThread(w io.Writer, detail threadDetail) {
	title := oneLine(detail.Title)
	if title == "" {
		title = "Thread " + detail.ID
	}
	fmt.Fprintf(w, "# %s\n\n", title)
	fmt.Fprintf(w, "id: %s\n", detail.ID)
	fmt.Fprintf(w, "Kind: %s\n", detail.Kind)
	if len(detail.Participants) > 0 {
		fmt.Fprintf(w, "Participants: %s\n", oneLine(strings.Join(detail.Participants, ", ")))
	}
	if !detail.CanReply {
		fmt.Fprintln(w, "This thread does not take replies.")
	}
	if detail.Truncated {
		fmt.Fprintln(w, "\nThere are older messages. Raise --limit to see them.")
	}
	if len(detail.Items) == 0 {
		fmt.Fprintln(w, "\nNo messages.")
		return
	}
	for _, m := range detail.Items {
		fmt.Fprintf(w, "\n## %s, %s\n\n", m.SentAt, oneLine(m.From))
		if m.Title != "" {
			fmt.Fprintf(w, "Title: %s\n\n", oneLine(m.Title))
		}
		fmt.Fprintln(w, ctlkit.Quote(m.Text))
		for _, file := range m.Attachments {
			fmt.Fprintf(w, "\nAttachment: %s (id %s)\n", oneLine(file.FileName), file.ID)
		}
	}
}

func renderReply(w io.Writer, result replyResult, detail threadDetail) {
	if result.DryRun {
		fmt.Fprintf(w, "Dry run. Would send this to %s. Nothing was sent.\n\n%s\n", threadName(detail), ctlkit.Quote(result.Request.Text))
		return
	}
	fmt.Fprintf(w, "Sent to %s.\n", threadName(detail))
}

func renderMarkRead(w io.Writer, result markReadResult) {
	if result.DryRun {
		fmt.Fprintf(w, "Dry run. Would mark thread %s as read. Nothing was sent.\n", result.ThreadID)
		return
	}
	fmt.Fprintf(w, "Marked thread %s as read.\n", result.ThreadID)
}

func renderOverview(w io.Writer, result overview) {
	fmt.Fprintln(w, "# Overview")
	if len(result.Items) == 0 {
		fmt.Fprintln(w, "\nNo children at a school.")
		return
	}
	for _, item := range result.Items {
		fmt.Fprintf(w, "\n## %s\n\n", oneLine(item.Child))
		fmt.Fprintf(w, "- Unread direct messages: %d\n", item.UnreadDirect)
		fmt.Fprintf(w, "- Unread group messages: %d\n", item.UnreadGroup)
		fmt.Fprintf(w, "- Unread notices: %d\n", item.UnreadNotices)
		fmt.Fprintf(w, "- Unanswered forms: %d\n", item.UnansweredForms)
		fmt.Fprintf(w, "- Unanswered consents: %d\n", item.UnansweredConsents)
		fmt.Fprintf(w, "- Applications waiting for an answer: %d\n", item.WaitingApplications)
		fmt.Fprintf(w, "- pupil id: %d\n", item.PupilID)
	}
}

func renderNotices(w io.Writer, list noticeList) {
	fmt.Fprintln(w, "# Notices")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo notices.")
		return
	}
	for _, n := range list.Items {
		unread := ""
		if n.Unread {
			unread = " (unread)"
		}
		fmt.Fprintf(w, "\n## %s%s\n\n", oneLine(n.Title), unread)
		fmt.Fprintf(w, "Sent: %s\nFrom: %s\nChild: %s\nKind: %s\n\n", n.SentAt, oneLine(n.Sender), oneLine(n.Child), n.Kind)
		fmt.Fprintln(w, ctlkit.Quote(n.Text))
	}
	if list.Truncated {
		fmt.Fprintln(w, "\nThere are more. Raise --limit to see them.")
	}
}

func renderAbsences(w io.Writer, list absenceList) {
	fmt.Fprintln(w, "# Absences")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo children with a current school year.")
		return
	}
	for _, report := range list.Items {
		fmt.Fprintf(w, "\n## %s, %s, %s\n\n", oneLine(report.Child), report.SchoolYear, report.Term)
		sum := report.Summary
		fmt.Fprintf(w, "Total: %d days and %d minutes\n", sum.TotalDays, sum.TotalMinutes)
		fmt.Fprintf(w, "Documented: %d days and %d minutes\n", sum.DocumentedDays, sum.DocumentedMinutes)
		fmt.Fprintf(w, "Not documented: %d days and %d minutes\n", sum.UndocumentedDays, sum.UndocumentedMinutes)
		if len(report.Absences) > 0 {
			fmt.Fprintln(w)
		}
		for _, entry := range report.Absences {
			documented := "not documented"
			if entry.Documented {
				documented = "documented"
			}
			fmt.Fprintf(w, "- %s: %s, %s\n", entry.Date, entry.Kind, documented)
			field(w, "Comment", entry.Comment)
			field(w, "Changed by", entry.ModifiedBy)
		}
	}
}

func renderForms(w io.Writer, list formList) {
	fmt.Fprintln(w, "# Forms")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo forms.")
		return
	}
	fmt.Fprintln(w)
	for _, f := range list.Items {
		state := "not answered"
		switch {
		case f.Answered:
			state = "answered"
		case f.DeadlinePassed:
			state = "not answered, and the deadline has passed"
		}
		fmt.Fprintf(w, "- %s\n", oneLine(f.Title))
		field(w, "State", state)
		field(w, "Deadline", f.Deadline)
		field(w, "Child", f.Child)
		field(w, "id", f.ID)
	}
}

func renderForm(w io.Writer, f formDetail) {
	fmt.Fprintf(w, "# %s\n\n", oneLine(f.Title))
	fmt.Fprintf(w, "id: %s\n", f.ID)
	if f.Deadline != "" {
		fmt.Fprintf(w, "Deadline: %s\n", f.Deadline)
	}
	if f.Answered {
		fmt.Fprintf(w, "Answered: %s by %s\n", f.AnsweredAt, oneLine(f.AnsweredBy))
	} else {
		fmt.Fprintln(w, "Not answered.")
	}
	if f.Text != "" {
		fmt.Fprintf(w, "\n%s\n", ctlkit.Quote(f.Text))
	}
	if f.MoreInfo != "" {
		fmt.Fprintf(w, "\n%s\n", ctlkit.Quote(f.MoreInfo))
	}
	if len(f.Alternatives) > 0 {
		fmt.Fprintln(w, "\nAlternatives:")
	}
	for _, alt := range f.Alternatives {
		mark := " "
		if alt.Selected {
			mark = "x"
		}
		fmt.Fprintf(w, "- [%s] %s\n", mark, oneLine(alt.Text))
	}
}

func renderConsents(w io.Writer, list consentList) {
	fmt.Fprintln(w, "# Consents")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo consents.")
		return
	}
	fmt.Fprintln(w)
	for _, k := range list.Items {
		fmt.Fprintf(w, "- %s\n", oneLine(k.Title))
		field(w, "Answer", k.Answer)
		field(w, "Child", k.Child)
		field(w, "Text", k.Text)
		field(w, "Changed", k.ModifiedAt)
		field(w, "Changed by", k.ModifiedBy)
	}
}

func renderApplications(w io.Writer, list applicationList) {
	fmt.Fprintln(w, "# Applications")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo applications.")
		return
	}
	fmt.Fprintln(w)
	for _, entry := range list.Items {
		fmt.Fprintf(w, "- %s: %s\n", entry.Type, entry.Status)
		field(w, "Sent", entry.CreatedAt)
		field(w, "Child", entry.Child)
		field(w, "Answered by", entry.AnsweredBy)
		field(w, "id", entry.ID)
	}
}

func renderApplication(w io.Writer, entry application) {
	fmt.Fprintf(w, "# Application for %s\n\n", entry.Type)
	fmt.Fprintf(w, "id: %s\nStatus: %s\nSent: %s\n", entry.ID, entry.Status, entry.CreatedAt)
	for _, line := range [][2]string{{"Sent by", entry.RequestedBy}, {"From", entry.From}, {"To", entry.To}, {"Cause", entry.Cause}, {"Answered", entry.AnsweredAt}, {"Answered by", entry.AnsweredBy}} {
		if line[1] != "" {
			fmt.Fprintf(w, "%s: %s\n", line[0], oneLine(line[1]))
		}
	}
	if entry.Comment != "" {
		fmt.Fprintf(w, "\nThe guardian wrote:\n\n%s\n", ctlkit.Quote(entry.Comment))
	}
	if entry.Answer != "" {
		fmt.Fprintf(w, "\nThe school answered:\n\n%s\n", ctlkit.Quote(entry.Answer))
	}
}

func renderClasses(w io.Writer, list classList) {
	fmt.Fprintln(w, "# Classes")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo children at a school.")
		return
	}
	for _, entry := range list.Items {
		fmt.Fprintf(w, "\n## %s\n\n", oneLine(entry.Child))
		for _, line := range [][2]string{{"Class", entry.Class}, {"Contact teacher", entry.ContactTeacher}, {"Teachers", strings.Join(entry.Teachers, ", ")}} {
			if line[1] != "" {
				fmt.Fprintf(w, "%s: %s\n", line[0], oneLine(line[1]))
			}
		}
		if len(entry.Subjects) > 0 {
			fmt.Fprintln(w)
		}
		for _, subject := range entry.Subjects {
			fmt.Fprintf(w, "- %s: %s\n", oneLine(subject.Subject), oneLine(strings.Join(subject.Teachers, ", ")))
		}
	}
}

func renderSchools(w io.Writer, list schoolList) {
	fmt.Fprintln(w, "# Schools")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo schools.")
		return
	}
	for _, entry := range list.Items {
		fmt.Fprintf(w, "\n## %s\n\n", oneLine(entry.Name))
		for _, line := range [][2]string{{"Phone", entry.Phone}, {"Email", entry.Email}, {"Address", entry.Address}, {"Homepage", entry.Homepage}} {
			if line[1] != "" {
				fmt.Fprintf(w, "%s: %s\n", line[0], oneLine(line[1]))
			}
		}
		if len(entry.Links) > 0 {
			fmt.Fprintln(w, "\nLinks:")
		}
		for _, link := range entry.Links {
			fmt.Fprintf(w, "- %s: %s\n", oneLine(link.Title), oneLine(link.URL))
		}
	}
}

func renderDocuments(w io.Writer, list documentList) {
	fmt.Fprintln(w, "# Documents")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo documents.")
		return
	}
	fmt.Fprintln(w)
	for _, file := range list.Items {
		fmt.Fprintf(w, "- %s\n", oneLine(file.Title))
		field(w, "Child", file.Child)
		if file.Scope == "school" {
			fmt.Fprintln(w, "  From the school, for every guardian")
		}
		field(w, "Created", file.CreatedAt)
		field(w, "Created by", file.CreatedBy)
		field(w, "id", file.ID)
	}
}

func (a *App) renderStatus(w io.Writer, status loginStatus) {
	if !status.LoggedIn {
		fmt.Fprintf(w, "Not logged in. %s\n", strings.ToUpper(loginHint[:1])+loginHint[1:])
		return
	}
	fmt.Fprintln(w, "Logged in.")
	if status.AccessExpires != "" {
		fmt.Fprintf(w, "Access token until: %s\n", a.local(status.AccessExpires))
	}
	if status.Renewable {
		fmt.Fprintln(w, "It renews itself when a command needs it. Visma Flyt Skole does not say for how long.")
	}
}

// local shows a time from the service in the user's zone, still as RFC 3339. A
// value that is not a time is shown as it is.
func (a *App) local(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.In(a.Zone).Format(time.RFC3339)
}

// field writes one indented line under a list item, and nothing for an
// empty value.
func field(w io.Writer, label, value string) {
	if value != "" {
		fmt.Fprintf(w, "  %s: %s\n", label, oneLine(value))
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
