package calendars

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
)

// --- Input/Output types ---

// ExportICSInput is the input for the authenticated iCal export endpoint.
type ExportICSInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
}

// ExportICSOutput streams an iCalendar file as the response body.
type ExportICSOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               string
}

// ShareExportICSInput is the input for the public share iCal export endpoint.
type ShareExportICSInput struct {
	Token string `path:"token" doc:"Invite token"`
}

// ShareExportICSOutput streams an iCalendar file via share token.
type ShareExportICSOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               string
}

// ExportICS generates an iCalendar (RFC 5545) file for all events in a calendar.
func ExportICS(deps Deps) func(context.Context, *ExportICSInput) (*ExportICSOutput, error) {
	return func(ctx context.Context, input *ExportICSInput) (*ExportICSOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		cal, _, err := resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		events, err := deps.Queries.ListAllCalendarEvents(ctx, cal.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list events for export", err)
		}

		ics := buildICS(cal.Name, events)
		safeName := sanitizeFilename(cal.Name)

		out := &ExportICSOutput{
			ContentType:        "text/calendar; charset=utf-8",
			ContentDisposition: fmt.Sprintf(`attachment; filename="%s.ics"`, safeName),
			Body:               ics,
		}
		return out, nil
	}
}

// ShareExportICS generates an iCalendar file for a shared calendar (no auth required).
func ShareExportICS(deps Deps) func(context.Context, *ShareExportICSInput) (*ShareExportICSOutput, error) {
	return func(ctx context.Context, input *ShareExportICSInput) (*ShareExportICSOutput, error) {
		invite, err := deps.Queries.FindCalendarInviteByToken(ctx, input.Token)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("Invite not found or expired")
			}
			return nil, huma.Error500InternalServerError("Failed to look up invite", err)
		}

		events, err := deps.Queries.ListAllCalendarEvents(ctx, invite.CalendarID)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list events for export", err)
		}

		ics := buildICS(invite.CalendarName, events)
		safeName := sanitizeFilename(invite.CalendarName)

		out := &ShareExportICSOutput{
			ContentType:        "text/calendar; charset=utf-8",
			ContentDisposition: fmt.Sprintf(`attachment; filename="%s.ics"`, safeName),
			Body:               ics,
		}
		return out, nil
	}
}

func buildICS(calendarName string, events []generated.ListAllCalendarEventsRow) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//Nodate Time//EN\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString(foldLine("X-WR-CALNAME:" + escapeICS(calendarName)))

	for _, e := range events {
		b.WriteString("BEGIN:VEVENT\r\n")
		b.WriteString(fmt.Sprintf("UID:%s@nodate-time\r\n", e.PublicID.String()))
		b.WriteString(foldLine("SUMMARY:" + escapeICS(e.Title)))

		if e.AllDay {
			b.WriteString(fmt.Sprintf("DTSTART;VALUE=DATE:%s\r\n", e.StartAt.Format("20060102")))
			b.WriteString(fmt.Sprintf("DTEND;VALUE=DATE:%s\r\n", e.EndAt.Format("20060102")))
		} else {
			b.WriteString(fmt.Sprintf("DTSTART:%s\r\n", e.StartAt.UTC().Format("20060102T150405Z")))
			b.WriteString(fmt.Sprintf("DTEND:%s\r\n", e.EndAt.UTC().Format("20060102T150405Z")))
		}

		if e.Location.Valid && e.Location.String != "" {
			b.WriteString(foldLine("LOCATION:" + escapeICS(e.Location.String)))
		}
		if e.Memo.Valid && e.Memo.String != "" {
			b.WriteString(foldLine("DESCRIPTION:" + escapeICS(e.Memo.String)))
		}
		if e.Url.Valid && e.Url.String != "" {
			b.WriteString(foldLine("URL:" + e.Url.String))
		}

		// TRANSP: busy=OPAQUE, free/tentative/oof=TRANSPARENT
		switch e.ShowAs {
		case generated.CalendarEventsShowAsBusy:
			b.WriteString("TRANSP:OPAQUE\r\n")
		default:
			b.WriteString("TRANSP:TRANSPARENT\r\n")
		}

		// Recurrence rule
		if e.RecurrenceRule != nil {
			rrule := buildRRule(e.RecurrenceRule)
			if rrule != "" {
				b.WriteString(foldLine("RRULE:" + rrule))
			}
		}

		// Notification as VALARM
		if e.NotificationOffset.Valid {
			b.WriteString("BEGIN:VALARM\r\n")
			b.WriteString("ACTION:DISPLAY\r\n")
			b.WriteString("DESCRIPTION:Reminder\r\n")
			b.WriteString(fmt.Sprintf("TRIGGER:-PT%dM\r\n", e.NotificationOffset.Int32))
			b.WriteString("END:VALARM\r\n")
		}

		if e.UpdatedAt.Valid {
			b.WriteString(fmt.Sprintf("LAST-MODIFIED:%s\r\n", e.UpdatedAt.Time.UTC().Format("20060102T150405Z")))
		}
		b.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", e.CreatedAt.UTC().Format("20060102T150405Z")))

		b.WriteString("END:VEVENT\r\n")
	}

	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

// buildRRule converts the JSON recurrence_rule into an iCal RRULE string.
func buildRRule(raw json.RawMessage) string {
	var rule struct {
		Freq       string   `json:"freq"`
		Interval   int      `json:"interval"`
		ByDay      []string `json:"byDay"`
		ByMonthDay []int    `json:"byMonthDay"`
		BySetPos   []int    `json:"bySetPos"`
		Until      string   `json:"until"`
		Count      int      `json:"count"`
	}
	if err := json.Unmarshal(raw, &rule); err != nil {
		return ""
	}
	if rule.Freq == "" {
		return ""
	}

	parts := []string{"FREQ=" + strings.ToUpper(rule.Freq)}
	if rule.Interval > 1 {
		parts = append(parts, fmt.Sprintf("INTERVAL=%d", rule.Interval))
	}
	if len(rule.ByDay) > 0 {
		parts = append(parts, "BYDAY="+strings.Join(rule.ByDay, ","))
	}
	if len(rule.ByMonthDay) > 0 {
		strs := make([]string, len(rule.ByMonthDay))
		for i, d := range rule.ByMonthDay {
			strs[i] = fmt.Sprintf("%d", d)
		}
		parts = append(parts, "BYMONTHDAY="+strings.Join(strs, ","))
	}
	if len(rule.BySetPos) > 0 {
		strs := make([]string, len(rule.BySetPos))
		for i, p := range rule.BySetPos {
			strs[i] = fmt.Sprintf("%d", p)
		}
		parts = append(parts, "BYSETPOS="+strings.Join(strs, ","))
	}
	if rule.Until != "" {
		t, err := time.Parse(time.RFC3339, rule.Until)
		if err == nil {
			parts = append(parts, "UNTIL="+t.UTC().Format("20060102T150405Z"))
		}
	}
	if rule.Count > 0 {
		parts = append(parts, fmt.Sprintf("COUNT=%d", rule.Count))
	}
	return strings.Join(parts, ";")
}

// escapeICS escapes special characters per RFC 5545 TEXT type.
func escapeICS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// foldLine implements RFC 5545 line folding (75 octet max per line).
func foldLine(line string) string {
	const maxLen = 75
	if len(line) <= maxLen {
		return line + "\r\n"
	}
	var b strings.Builder
	for len(line) > 0 {
		cut := maxLen
		if b.Len() > 0 {
			// Continuation lines start with a space
			b.WriteString(" ")
			cut = maxLen - 1
		}
		if cut > len(line) {
			cut = len(line)
		}
		b.WriteString(line[:cut])
		b.WriteString("\r\n")
		line = line[cut:]
	}
	return b.String()
}

// sanitizeFilename removes characters unsafe for filenames.
func sanitizeFilename(name string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	s := r.Replace(name)
	if s == "" {
		s = "calendar"
	}
	return s
}
