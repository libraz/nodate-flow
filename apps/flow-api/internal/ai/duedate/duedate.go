// Package duedate compares a task's due_on to "today" for the
// deterministic AI evaluators (reminders, stateinfer, autoactions,
// priorityopt, digest), so all of them agree on when a task is due today
// and when it is overdue.
//
// due_on is a calendar date: the driver hands it over as midnight in its
// own location, and comparing that instant with the current instant makes
// a task due today overdue from 00:00 UTC. Here both sides are calendar
// dates, today being the date the current instant falls on in the zone
// whose day it is.
package duedate

import (
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// DaysUntil returns the number of calendar days from today (now read in
// z) to dueOn: 0 when it is due today, negative when overdue.
func DaysUntil(dueOn, now time.Time, z region.Zone) int {
	return region.DayFromDateColumn(dueOn).Sub(region.DayOf(now, z))
}

// Overdue reports whether dueOn is strictly before today (now read in z).
// A task due today is not overdue at any hour of that day.
func Overdue(dueOn, now time.Time, z region.Zone) bool {
	return DaysUntil(dueOn, now, z) < 0
}
