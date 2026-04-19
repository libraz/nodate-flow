package calendars

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
)

// --- Input/Output types ---

// SmartCreateInput is the input for the smart event creation endpoint.
type SmartCreateInput struct {
	WsId  string `path:"wsId" doc:"Workspace public ID"`
	CalId string `path:"calId" doc:"Calendar public ID"`
	Body  struct {
		Text     string `json:"text" minLength:"1" maxLength:"1000" doc:"Natural language event description"`
		Timezone string `json:"timezone,omitempty" doc:"IANA timezone (e.g. America/New_York). Defaults to Asia/Tokyo if omitted."`
	}
}

// EventProposal is a proposed event extracted from natural language text.
type EventProposal struct {
	Title   string `json:"title"`
	StartAt int64  `json:"startAt"`
	EndAt   int64  `json:"endAt"`
	Kind    string `json:"kind"`
	ShowAs  string `json:"showAs"`
}

// SmartCreateOutput is the response for the smart event creation endpoint.
type SmartCreateOutput struct {
	Body struct {
		Proposal EventProposal `json:"proposal"`
	}
}

// SmartCreate parses natural language text into an event proposal without
// actually creating the event. The client can confirm by sending a regular
// POST /events request.
func SmartCreate(deps Deps) func(context.Context, *SmartCreateInput) (*SmartCreateOutput, error) {
	return func(ctx context.Context, input *SmartCreateInput) (*SmartCreateOutput, error) {
		wsID, actorID, err := resolveWorkspace(ctx, deps.Queries, input.WsId)
		if err != nil {
			return nil, err
		}
		_, _, err = resolveCalendar(ctx, deps.Queries, wsID, actorID, input.CalId)
		if err != nil {
			return nil, err
		}

		tz := input.Body.Timezone
		if tz == "" {
			tz = "Asia/Tokyo"
		}
		proposal, err := ParseEventFromText(input.Body.Text, time.Now(), tz)
		if err != nil {
			return nil, httpErr(apierrors.CalendarSmartCreateTextUnparseable)
		}

		out := &SmartCreateOutput{}
		out.Body.Proposal = *proposal
		return out, nil
	}
}

// ParseEventFromText extracts event parameters from natural language.
// This is a simple rule-based parser. Can be replaced with LLM later.
// The timezone parameter is an IANA timezone name (e.g. "America/New_York").
func ParseEventFromText(text string, now time.Time, timezone string) (*EventProposal, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	nowJST := now.In(loc)

	remaining := text

	// Extract date
	date := nowJST
	remaining, date = extractDate(remaining, nowJST)

	// Extract start time
	startHour, startMin, hasStart := extractStartTime(remaining)
	if hasStart {
		date = time.Date(date.Year(), date.Month(), date.Day(), startHour, startMin, 0, 0, loc)
	} else {
		date = time.Date(date.Year(), date.Month(), date.Day(), 9, 0, 0, 0, loc)
	}

	// Extract end time
	endHour, endMin, hasEnd := extractEndTime(remaining)
	var endAt time.Time
	if hasEnd {
		endAt = time.Date(date.Year(), date.Month(), date.Day(), endHour, endMin, 0, 0, loc)
		if endAt.Before(date) {
			endAt = endAt.Add(24 * time.Hour)
		}
	} else {
		endAt = date.Add(time.Hour)
	}

	// Build the title from the remaining text after removing time/date patterns
	title := cleanTitle(remaining)
	if title == "" {
		title = "New Event"
	}

	return &EventProposal{
		Title:   title,
		StartAt: date.Unix(),
		EndAt:   endAt.Unix(),
		Kind:    "event",
		ShowAs:  "busy",
	}, nil
}

var (
	// Date patterns
	reNextWeekday = regexp.MustCompile(`来週(月|火|水|木|金|土|日)曜日?`)
	reTomorrow    = regexp.MustCompile(`明日`)
	reDayAfter    = regexp.MustCompile(`明後日`)
	reToday       = regexp.MustCompile(`今日`)
	reThisWeekday = regexp.MustCompile(`今週(月|火|水|木|金|土|日)曜日?`)
	reWeekday     = regexp.MustCompile(`(月|火|水|木|金|土|日)曜日?`)

	// Time patterns
	reTimeJP     = regexp.MustCompile(`(\d{1,2})時(?:(\d{1,2})分)?`)
	reTimeColon  = regexp.MustCompile(`(\d{1,2}):(\d{2})`)
	reEndTimeJP  = regexp.MustCompile(`(?:〜|～|から|まで)(\d{1,2})時(?:(\d{1,2})分)?(?:まで)?`)
	reEndColon   = regexp.MustCompile(`(?:〜|～|から|まで)(\d{1,2}):(\d{2})`)
	reRangeJP    = regexp.MustCompile(`(\d{1,2})時(?:(\d{1,2})分)?(?:〜|～|から)(\d{1,2})時(?:(\d{1,2})分)?`)
	reRangeColon = regexp.MustCompile(`(\d{1,2}):(\d{2})(?:〜|～|から)(\d{1,2}):(\d{2})`)

	// All time/date patterns for title cleanup
	reAllPatterns = regexp.MustCompile(
		`来週[月火水木金土日]曜日?|明後日|明日|今日|今週[月火水木金土日]曜日?|[月火水木金土日]曜日?` +
			`|(?:〜|～|から|まで)?\d{1,2}時(?:\d{1,2}分)?(?:まで)?` +
			`|\d{1,2}:\d{2}` +
			`|(?:〜|～|から|まで)\d{1,2}:\d{2}` +
			`|の$|に$`)
)

var weekdayMap = map[string]time.Weekday{
	"月": time.Monday,
	"火": time.Tuesday,
	"水": time.Wednesday,
	"木": time.Thursday,
	"金": time.Friday,
	"土": time.Saturday,
	"日": time.Sunday,
}

func extractDate(text string, now time.Time) (string, time.Time) {
	if m := reNextWeekday.FindStringSubmatch(text); m != nil {
		wd := weekdayMap[m[1]]
		d := nextWeekdayFrom(now, wd, true)
		return reNextWeekday.ReplaceAllString(text, ""), d
	}
	if reDayAfter.MatchString(text) {
		return reDayAfter.ReplaceAllString(text, ""), now.AddDate(0, 0, 2)
	}
	if reTomorrow.MatchString(text) {
		return reTomorrow.ReplaceAllString(text, ""), now.AddDate(0, 0, 1)
	}
	if reToday.MatchString(text) {
		return reToday.ReplaceAllString(text, ""), now
	}
	if m := reThisWeekday.FindStringSubmatch(text); m != nil {
		wd := weekdayMap[m[1]]
		d := nextWeekdayFrom(now, wd, false)
		return reThisWeekday.ReplaceAllString(text, ""), d
	}
	if m := reWeekday.FindStringSubmatch(text); m != nil {
		wd := weekdayMap[m[1]]
		d := nextWeekdayFrom(now, wd, false)
		return reWeekday.ReplaceAllString(text, ""), d
	}
	return text, now
}

func nextWeekdayFrom(from time.Time, wd time.Weekday, nextWeek bool) time.Time {
	current := from.Weekday()
	daysAhead := int(wd) - int(current)
	if nextWeek {
		// "来週X曜" should always be 7-13 days ahead
		daysAhead += 7
		if daysAhead < 7 {
			daysAhead += 7
		}
		if daysAhead > 13 {
			daysAhead -= 7
		}
	} else {
		if daysAhead <= 0 {
			daysAhead += 7
		}
	}
	return from.AddDate(0, 0, daysAhead)
}

func extractStartTime(text string) (int, int, bool) {
	// Check for range patterns first (take the start part)
	if m := reRangeColon.FindStringSubmatch(text); m != nil {
		h := atoi(m[1])
		mi := atoi(m[2])
		if h >= 0 && h <= 23 && mi >= 0 && mi <= 59 {
			return h, mi, true
		}
	}
	if m := reRangeJP.FindStringSubmatch(text); m != nil {
		h := atoi(m[1])
		mi := 0
		if m[2] != "" {
			mi = atoi(m[2])
		}
		if h >= 0 && h <= 23 && mi >= 0 && mi <= 59 {
			return h, mi, true
		}
	}
	// Single time patterns
	if m := reTimeColon.FindStringSubmatch(text); m != nil {
		h := atoi(m[1])
		mi := atoi(m[2])
		if h >= 0 && h <= 23 && mi >= 0 && mi <= 59 {
			return h, mi, true
		}
	}
	if m := reTimeJP.FindStringSubmatch(text); m != nil {
		h := atoi(m[1])
		mi := 0
		if m[2] != "" {
			mi = atoi(m[2])
		}
		if h >= 0 && h <= 23 && mi >= 0 && mi <= 59 {
			return h, mi, true
		}
	}
	return 0, 0, false
}

func extractEndTime(text string) (int, int, bool) {
	if m := reRangeColon.FindStringSubmatch(text); m != nil {
		h := atoi(m[3])
		mi := atoi(m[4])
		if h >= 0 && h <= 23 && mi >= 0 && mi <= 59 {
			return h, mi, true
		}
	}
	if m := reRangeJP.FindStringSubmatch(text); m != nil {
		h := atoi(m[3])
		mi := 0
		if m[4] != "" {
			mi = atoi(m[4])
		}
		if h >= 0 && h <= 23 && mi >= 0 && mi <= 59 {
			return h, mi, true
		}
	}
	if m := reEndColon.FindStringSubmatch(text); m != nil {
		h := atoi(m[1])
		mi := atoi(m[2])
		if h >= 0 && h <= 23 && mi >= 0 && mi <= 59 {
			return h, mi, true
		}
	}
	if m := reEndTimeJP.FindStringSubmatch(text); m != nil {
		h := atoi(m[1])
		mi := 0
		if m[2] != "" {
			mi = atoi(m[2])
		}
		if h >= 0 && h <= 23 && mi >= 0 && mi <= 59 {
			return h, mi, true
		}
	}
	return 0, 0, false
}

func cleanTitle(text string) string {
	cleaned := reAllPatterns.ReplaceAllString(text, "")
	// Remove common particles and connectors left dangling
	cleaned = regexp.MustCompile(`^[\s　のに]+|[\s　のに]+$`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`[\s　]+`).ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)
	// Ensure we have at least something
	if utf8.RuneCountInString(cleaned) == 0 {
		return ""
	}
	return cleaned
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
