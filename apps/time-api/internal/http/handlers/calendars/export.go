package calendars

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

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

		var buf bytes.Buffer
		if err := buildICS(&buf, cal.Name, events); err != nil {
			return nil, huma.Error500InternalServerError("Failed to build iCal export", err)
		}
		safeName := sanitizeFilename(cal.Name)

		out := &ExportICSOutput{
			ContentType:        "text/calendar; charset=utf-8",
			ContentDisposition: fmt.Sprintf(`attachment; filename="%s.ics"`, safeName),
			Body:               buf.String(),
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

		if err := validateInvite(invite.ExpiresAt, invite.MaxUses, invite.UseCount); err != nil {
			return nil, err
		}

		events, err := deps.Queries.ListAllCalendarEvents(ctx, invite.CalendarID)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to list events for export", err)
		}

		var buf bytes.Buffer
		if err := buildICS(&buf, invite.CalendarName, events); err != nil {
			return nil, huma.Error500InternalServerError("Failed to build iCal export", err)
		}
		safeName := sanitizeFilename(invite.CalendarName)

		out := &ShareExportICSOutput{
			ContentType:        "text/calendar; charset=utf-8",
			ContentDisposition: fmt.Sprintf(`attachment; filename="%s.ics"`, safeName),
			Body:               buf.String(),
		}
		return out, nil
	}
}

// buildICS writes an iCalendar (RFC 5545) document to w.
//
// The function accepts io.Writer so that callers can stream directly to an
// http.ResponseWriter in the future. Currently Huma requires the full body
// in memory (ExportICSOutput.Body string), so handlers pass a bytes.Buffer.
// When Huma streaming support is available, this function can write directly
// to the response without buffering.
func buildICS(w io.Writer, calendarName string, events []generated.ListAllCalendarEventsRow) error {
	wr := func(s string) error {
		_, err := io.WriteString(w, s)
		return err
	}
	wrf := func(format string, args ...any) error {
		_, err := fmt.Fprintf(w, format, args...)
		return err
	}

	if err := wr("BEGIN:VCALENDAR\r\n"); err != nil {
		return err
	}
	if err := wr("VERSION:2.0\r\n"); err != nil {
		return err
	}
	if err := wr("PRODID:-//Nodate Time//EN\r\n"); err != nil {
		return err
	}
	if err := wr("CALSCALE:GREGORIAN\r\n"); err != nil {
		return err
	}
	if err := wr(foldLine("X-WR-CALNAME:" + escapeICS(calendarName))); err != nil {
		return err
	}

	for _, e := range events {
		if err := wr("BEGIN:VEVENT\r\n"); err != nil {
			return err
		}
		if err := wrf("UID:%s@nodate-time\r\n", e.PublicID.String()); err != nil {
			return err
		}
		if err := wr(foldLine("SUMMARY:" + escapeICS(e.Title))); err != nil {
			return err
		}

		if e.AllDay {
			if err := wrf("DTSTART;VALUE=DATE:%s\r\n", e.StartAt.Format("20060102")); err != nil {
				return err
			}
			if err := wrf("DTEND;VALUE=DATE:%s\r\n", e.EndAt.Format("20060102")); err != nil {
				return err
			}
		} else {
			if err := wrf("DTSTART:%s\r\n", e.StartAt.UTC().Format("20060102T150405Z")); err != nil {
				return err
			}
			if err := wrf("DTEND:%s\r\n", e.EndAt.UTC().Format("20060102T150405Z")); err != nil {
				return err
			}
		}

		if e.Location.Valid && e.Location.String != "" {
			if err := wr(foldLine("LOCATION:" + escapeICS(e.Location.String))); err != nil {
				return err
			}
		}
		if e.Memo.Valid && e.Memo.String != "" {
			if err := wr(foldLine("DESCRIPTION:" + escapeICS(e.Memo.String))); err != nil {
				return err
			}
		}
		if e.Url.Valid && e.Url.String != "" {
			if err := wr(foldLine("URL:" + e.Url.String)); err != nil {
				return err
			}
		}

		// TRANSP: busy=OPAQUE, free/tentative/oof=TRANSPARENT
		switch e.ShowAs {
		case generated.CalendarEventsShowAsBusy:
			if err := wr("TRANSP:OPAQUE\r\n"); err != nil {
				return err
			}
		default:
			if err := wr("TRANSP:TRANSPARENT\r\n"); err != nil {
				return err
			}
		}

		// Recurrence rule
		if e.RecurrenceRule != nil {
			rrule := buildRRule(e.RecurrenceRule)
			if rrule != "" {
				if err := wr(foldLine("RRULE:" + rrule)); err != nil {
					return err
				}
			}
		}

		// Recurrence exceptions (EXDATE)
		if e.RecurrenceExceptions != nil {
			var exdates []string
			if jsonErr := json.Unmarshal(e.RecurrenceExceptions, &exdates); jsonErr == nil {
				for _, exStr := range exdates {
					t, parseErr := time.Parse(time.RFC3339, exStr)
					if parseErr == nil {
						if e.AllDay {
							if err := wrf("EXDATE;VALUE=DATE:%s\r\n", t.Format("20060102")); err != nil {
								return err
							}
						} else {
							if err := wrf("EXDATE:%s\r\n", t.UTC().Format("20060102T150405Z")); err != nil {
								return err
							}
						}
					}
				}
			}
		}

		// Notification as VALARM
		if e.NotificationOffset.Valid {
			if err := wr("BEGIN:VALARM\r\n"); err != nil {
				return err
			}
			if err := wr("ACTION:DISPLAY\r\n"); err != nil {
				return err
			}
			if err := wr("DESCRIPTION:Reminder\r\n"); err != nil {
				return err
			}
			if err := wrf("TRIGGER:-PT%dM\r\n", e.NotificationOffset.Int32); err != nil {
				return err
			}
			if err := wr("END:VALARM\r\n"); err != nil {
				return err
			}
		}

		if e.UpdatedAt.Valid {
			if err := wrf("LAST-MODIFIED:%s\r\n", e.UpdatedAt.Time.UTC().Format("20060102T150405Z")); err != nil {
				return err
			}
		}
		if err := wrf("DTSTAMP:%s\r\n", e.CreatedAt.UTC().Format("20060102T150405Z")); err != nil {
			return err
		}

		if err := wr("END:VEVENT\r\n"); err != nil {
			return err
		}
	}

	return wr("END:VCALENDAR\r\n")
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
// It respects UTF-8 multi-byte boundaries so that no rune is split across
// folded lines.
func foldLine(line string) string {
	const maxLen = 75
	if len(line) <= maxLen {
		return line + "\r\n"
	}
	var b strings.Builder
	first := true
	for len(line) > 0 {
		limit := maxLen
		if !first {
			b.WriteString(" ")
			limit = maxLen - 1
		}
		if len(line) <= limit {
			b.WriteString(line)
			b.WriteString("\r\n")
			break
		}
		// Walk back from limit to find a valid UTF-8 boundary
		cut := limit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		if cut == 0 {
			// Single rune wider than limit (shouldn't happen with UTF-8, but be safe)
			_, size := utf8.DecodeRuneInString(line)
			cut = size
		}
		b.WriteString(line[:cut])
		b.WriteString("\r\n")
		line = line[cut:]
		first = false
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
