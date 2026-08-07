package prefs

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

const (
	inApp = generated.NotificationsChannelInApp
	mail  = generated.NotificationsChannelEmail
	push  = generated.NotificationsChannelPush
)

func TestResolveChannels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		prefs []Pref
		want  []generated.NotificationsChannel
	}{
		{
			name:  "no rows delivers in-app only",
			prefs: nil,
			want:  []generated.NotificationsChannel{inApp},
		},
		{
			// The defect this rule replaces: a mute stored on the
			// default channel used to be dropped, because the reader
			// could not tell "muted" from "never configured" and
			// applied the default to both.
			name:  "muting in-app silences the category",
			prefs: []Pref{{Channel: inApp, Muted: true}},
			want:  []generated.NotificationsChannel{},
		},
		{
			name:  "opting into email keeps in-app",
			prefs: []Pref{{Channel: mail, Muted: false}},
			want:  []generated.NotificationsChannel{inApp, mail},
		},
		{
			name:  "email stays off until asked for",
			prefs: []Pref{{Channel: inApp, Muted: false}},
			want:  []generated.NotificationsChannel{inApp},
		},
		{
			name: "a row for every channel is honoured verbatim",
			prefs: []Pref{
				{Channel: inApp, Muted: true},
				{Channel: mail, Muted: false},
				{Channel: push, Muted: false},
			},
			want: []generated.NotificationsChannel{mail, push},
		},
		{
			name: "everything muted delivers nothing",
			prefs: []Pref{
				{Channel: inApp, Muted: true},
				{Channel: mail, Muted: true},
				{Channel: push, Muted: true},
			},
			want: []generated.NotificationsChannel{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveChannels(tc.prefs)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestCategoriesCoverEveryRoutedCategory guards the pairing between the
// list a user can configure and the categories fan-out routes events
// to. A category reachable from the fan-out but absent from Categories
// would carry events nobody could ever mute.
func TestCategoriesCoverEveryRoutedCategory(t *testing.T) {
	t.Parallel()

	for _, c := range Categories {
		if !ValidCategory(c) {
			t.Fatalf("category %q is listed but fails validation", c)
		}
	}
	if ValidCategory("") || ValidCategory("task") {
		t.Fatal("ValidCategory must reject categories outside the list")
	}
	if !ValidChannel("in_app") || !ValidChannel("email") || !ValidChannel("push") {
		t.Fatal("ValidChannel must accept every declared channel")
	}
	if ValidChannel("sms") {
		t.Fatal("ValidChannel must reject undeclared channels")
	}
}
