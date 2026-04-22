package eventacl

import "testing"

func TestCanSee(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		evt  Event
		act  Actor
		want bool
	}{
		{"non-member sees nothing",
			Event{Visibility: VisibilityPublic},
			Actor{UserID: 1, IsWorkspaceMember: false},
			false,
		},
		{"system calendar is public to ws members",
			Event{Visibility: VisibilityConfidential, CalendarKind: CalendarKindSystem},
			Actor{UserID: 1, IsWorkspaceMember: true},
			true,
		},
		{"owner sees own confidential",
			Event{Visibility: VisibilityConfidential, OwnerUserID: 42},
			Actor{UserID: 42, IsWorkspaceMember: true},
			true,
		},
		{"calendar owner sees event in their layer",
			Event{Visibility: VisibilityConfidential, OwnerUserID: 99, CalendarOwnerID: 42},
			Actor{UserID: 42, IsWorkspaceMember: true},
			true,
		},
		{"public visible to any ws member",
			Event{Visibility: VisibilityPublic, OwnerUserID: 99},
			Actor{UserID: 7, IsWorkspaceMember: true},
			true,
		},
		{"default treated as public",
			Event{Visibility: VisibilityDefault, OwnerUserID: 99},
			Actor{UserID: 7, IsWorkspaceMember: true},
			true,
		},
		{"private visible to attendee",
			Event{Visibility: VisibilityPrivate, OwnerUserID: 99},
			Actor{UserID: 7, IsWorkspaceMember: true, IsAttendee: true},
			true,
		},
		{"private hidden from non-attendee",
			Event{Visibility: VisibilityPrivate, OwnerUserID: 99},
			Actor{UserID: 7, IsWorkspaceMember: true, IsAttendee: false},
			false,
		},
		{"confidential hidden from non-owner",
			Event{Visibility: VisibilityConfidential, OwnerUserID: 99},
			Actor{UserID: 7, IsWorkspaceMember: true, IsAttendee: true},
			false,
		},
		{"empty visibility treated as default",
			Event{Visibility: "", OwnerUserID: 99},
			Actor{UserID: 7, IsWorkspaceMember: true},
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanSee(c.evt, c.act); got != c.want {
				t.Fatalf("CanSee(%v, %v): want %v, got %v", c.evt, c.act, c.want, got)
			}
		})
	}
}
