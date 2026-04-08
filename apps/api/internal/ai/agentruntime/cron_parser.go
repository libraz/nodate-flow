package agentruntime

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// CronExpr is a parsed 5-field cron expression: `minute hour dom month
// dow`. Each field supports `*` (any) and a comma-separated list of
// integers within the field's domain. Ranges (`1-5`) and steps
// (`*/5`) are intentionally out of scope for the first cut — add them
// when a real agent needs them.
type CronExpr struct {
	minute []int // 0..59, nil = any
	hour   []int // 0..23
	dom    []int // 1..31
	month  []int // 1..12
	dow    []int // 0..6  (0 = Sunday)
}

// ErrInvalidCron is returned by [ParseCron] when the expression does
// not parse. Callers typically log and skip the row.
var ErrInvalidCron = errors.New("agentruntime: invalid cron expression")

// ParseCron parses a 5-field expression. Whitespace between fields is
// collapsed, so leading/trailing spaces and tab-separated inputs
// round-trip unchanged.
func ParseCron(expr string) (CronExpr, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return CronExpr{}, ErrInvalidCron
	}
	minute, err := parseField(fields[0], 0, 59)
	if err != nil {
		return CronExpr{}, err
	}
	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return CronExpr{}, err
	}
	dom, err := parseField(fields[2], 1, 31)
	if err != nil {
		return CronExpr{}, err
	}
	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return CronExpr{}, err
	}
	dow, err := parseField(fields[4], 0, 6)
	if err != nil {
		return CronExpr{}, err
	}
	return CronExpr{minute: minute, hour: hour, dom: dom, month: month, dow: dow}, nil
}

// Matches reports whether now falls inside the expression's schedule.
// The evaluator is minute-resolution; seconds are ignored.
func (c CronExpr) Matches(now time.Time) bool {
	return inSet(c.minute, now.Minute()) &&
		inSet(c.hour, now.Hour()) &&
		inSet(c.dom, now.Day()) &&
		inSet(c.month, int(now.Month())) &&
		inSet(c.dow, int(now.Weekday()))
}

func parseField(raw string, lo, hi int) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrInvalidCron
	}
	if raw == "*" {
		return nil, nil // nil sentinel = "any"
	}
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < lo || n > hi {
			return nil, ErrInvalidCron
		}
		out = append(out, n)
	}
	return out, nil
}

func inSet(set []int, v int) bool {
	if set == nil {
		return true
	}
	for _, n := range set {
		if n == v {
			return true
		}
	}
	return false
}
