// Package prefs holds the notification preference vocabulary — the
// configurable event categories, the delivery channels, and the rule
// that resolves a user's stored rows into the channels a notification
// is actually written to.
//
// It sits below both the fan-out that delivers notifications and the
// HTTP handler that edits the settings, so the screen a user sees and
// the code that honours it cannot drift apart. The parent notification
// package cannot host it: that package's own tests reach the router,
// which reaches the handlers, which would reach back here.
package prefs

import "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"

// Event categories are the buckets users configure. Dozens of event
// types collapse onto this handful so a preference screen stays a
// screen; the notification package's categoryForEventType owns the
// mapping from event type to category.
const (
	CategoryTaskLifecycle = "task.lifecycle"
	CategoryTaskComment   = "task.comment"
	CategoryTaskMention   = "task.mention"
	CategoryRelation      = "relation"
	CategoryTimebox       = "timebox"
	CategoryAI            = "ai"
)

// Categories lists every configurable event category in display order.
// It is the validation set for preference writes and the row set the
// preferences endpoint renders, so a category the fan-out routes events
// to but which is missing here would be silently unconfigurable.
var Categories = []string{
	CategoryTaskLifecycle,
	CategoryTaskComment,
	CategoryTaskMention,
	CategoryRelation,
	CategoryTimebox,
	CategoryAI,
}

// Channels lists every delivery channel in display order.
var Channels = []generated.NotificationsChannel{
	generated.NotificationsChannelInApp,
	generated.NotificationsChannelEmail,
	generated.NotificationsChannelPush,
}

// Pref is one stored notification_preferences row reduced to the two
// fields channel resolution needs.
type Pref struct {
	Channel generated.NotificationsChannel
	Muted   bool
}

// ChannelMuted reports whether a channel is suppressed for a category,
// given the stored rows for that (user, category) pair.
//
// The defaults are asymmetric on purpose. in_app is the bell inside the
// product: it is on unless the user says otherwise, which is what makes
// a stored mute meaningful — a user who mutes a category has no row
// saying "deliver", only one saying "do not", and a default of "off"
// would make that mute unrepresentable. email and push leave the
// product and reach the person, so they stay off until the user opts
// in; an absent row must never be read as consent to mail somebody.
func ChannelMuted(prefs []Pref, channel generated.NotificationsChannel) bool {
	muted := channel != generated.NotificationsChannelInApp
	for _, p := range prefs {
		if p.Channel == channel {
			muted = p.Muted
		}
	}
	return muted
}

// ResolveChannels returns the channels a notification for one category
// should be written to, in [Channels] order. An empty result means the
// user has silenced the category entirely and no notification row is
// created for them.
func ResolveChannels(prefs []Pref) []generated.NotificationsChannel {
	out := make([]generated.NotificationsChannel, 0, len(Channels))
	for _, ch := range Channels {
		if !ChannelMuted(prefs, ch) {
			out = append(out, ch)
		}
	}
	return out
}

// ValidCategory reports whether s is a configurable event category.
func ValidCategory(s string) bool {
	for _, c := range Categories {
		if c == s {
			return true
		}
	}
	return false
}

// ValidChannel reports whether s names a delivery channel.
func ValidChannel(s string) bool {
	for _, c := range Channels {
		if string(c) == s {
			return true
		}
	}
	return false
}
