package eventacl

import "testing"

// The two predicates are tested separately because the bug they replace
// came from treating them as one. A table that only asserted "can the
// actor see it" cannot express the case the product actually has:
// a private event whose time is visible to the whole calendar and whose
// room is not.

func TestCanSee(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		evt  Event
		act  Actor
		want bool
	}{
		{"public is a row anyone on the calendar may see",
			Event{Visibility: VisibilityPublic, OwnerUserID: 99},
			Actor{UserID: 7},
			true,
		},
		{"default is treated as public",
			Event{Visibility: VisibilityDefault, OwnerUserID: 99},
			Actor{UserID: 7},
			true,
		},
		{"empty visibility is treated as public",
			Event{Visibility: "", OwnerUserID: 99},
			Actor{UserID: 7},
			true,
		},
		{"private is still a visible row: the time is taken",
			Event{Visibility: VisibilityPrivate, OwnerUserID: 99},
			Actor{UserID: 7},
			true,
		},
		{"confidential is hidden from a co-member",
			Event{Visibility: VisibilityConfidential, OwnerUserID: 99},
			Actor{UserID: 7},
			false,
		},
		{"confidential is visible to its owner",
			Event{Visibility: VisibilityConfidential, OwnerUserID: 42},
			Actor{UserID: 42},
			true,
		},
		{"attendance does not open a confidential event",
			Event{Visibility: VisibilityConfidential, OwnerUserID: 99},
			Actor{UserID: 7, IsAttendee: true},
			false,
		},
		{"a zero actor id owns nothing",
			Event{Visibility: VisibilityConfidential, OwnerUserID: 0},
			Actor{UserID: 0},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanSee(c.evt, c.act); got != c.want {
				t.Fatalf("CanSee(%+v, %+v) = %v, want %v", c.evt, c.act, got, c.want)
			}
		})
	}
}

func TestCanSeeDetails(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		evt  Event
		act  Actor
		want bool
	}{
		{"public details are readable by any calendar member",
			Event{Visibility: VisibilityPublic, OwnerUserID: 99},
			Actor{UserID: 7},
			true,
		},
		{"private details are withheld from a co-member",
			Event{Visibility: VisibilityPrivate, OwnerUserID: 99},
			Actor{UserID: 7},
			false,
		},
		{"private details reach the people invited",
			Event{Visibility: VisibilityPrivate, OwnerUserID: 99},
			Actor{UserID: 7, IsAttendee: true},
			true,
		},
		{"private details reach the owner",
			Event{Visibility: VisibilityPrivate, OwnerUserID: 42},
			Actor{UserID: 42},
			true,
		},
		{"confidential details follow the hidden row",
			Event{Visibility: VisibilityConfidential, OwnerUserID: 99},
			Actor{UserID: 7, IsAttendee: true},
			false,
		},
		{"confidential details reach its owner",
			Event{Visibility: VisibilityConfidential, OwnerUserID: 42},
			Actor{UserID: 42},
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanSeeDetails(c.evt, c.act); got != c.want {
				t.Fatalf("CanSeeDetails(%+v, %+v) = %v, want %v", c.evt, c.act, got, c.want)
			}
		})
	}
}

// TestDetailsNeverExceedRowVisibility states the relationship between
// the two predicates as a property rather than a list: there is no
// combination in which the details are readable but the row is not.
func TestDetailsNeverExceedRowVisibility(t *testing.T) {
	t.Parallel()
	visibilities := []Visibility{VisibilityPublic, VisibilityDefault, VisibilityPrivate, VisibilityConfidential, ""}
	owners := []uint32{0, 42, 99}
	actors := []uint32{0, 42, 7}
	for _, v := range visibilities {
		for _, owner := range owners {
			for _, uid := range actors {
				for _, attendee := range []bool{false, true} {
					evt := Event{Visibility: v, OwnerUserID: owner}
					act := Actor{UserID: uid, IsAttendee: attendee}
					if CanSeeDetails(evt, act) && !CanSee(evt, act) {
						t.Fatalf("details readable on an invisible row: %+v %+v", evt, act)
					}
				}
			}
		}
	}
}
