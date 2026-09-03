package region

import "time"

// Zone is a resolved IANA timezone: a name that was found in the zoneinfo
// database, paired with the [time.Location] it resolved to.
//
// It exists so that "which zone applies here" is answered once, at the
// point where the user and workspace preferences are readable, and then
// carried by value to everything downstream. A `string` cannot carry that
// answer: the empty string is a valid value of the type, so every helper
// taking one has to invent a policy for it, and independent inventions do
// not agree. The same stored row then dates differently depending on
// which helper reads it.
//
// The zero Zone is not a zone. It is what a composite literal produces,
// and [TestZoneResolutionIsCentralised] refuses one written outside this
// package, so the only way to obtain a usable value is [Resolve] or
// [UTC].
type Zone struct {
	name string
	loc  *time.Location
}

// Resolve is the single timezone resolution in the product.
//
// Candidates are supplied most-specific first — the explicit value on the
// request, then the actor's preference, then the workspace's — and the
// first non-empty one wins. When every candidate is empty the result is
// [DefaultTimezone], which is the same fallback the schema states for
// `calendar_events.timezone` and for the `timezone` column on users and
// workspaces.
//
// A winning candidate that the zoneinfo database does not know is an
// error rather than a fallback. Falling back is what makes such a defect
// invisible: a row naming a zone that no longer exists would keep
// producing dates, one day off, with nothing logged and nobody told.
func Resolve(candidates ...string) (Zone, error) {
	name := EffectiveTimezone(candidates...)
	loc, err := LoadLocation(name)
	if err != nil {
		return Zone{}, err
	}
	return Zone{name: name, loc: loc}, nil
}

// UTC returns the zone every fallback in the chain ends at.
//
// Call it where UTC is the answer rather than the absence of one: the
// all-day normalisation on the way into `calendar_events`, whose stored
// instant is defined as midnight UTC on the author's date, is the case
// this exists for. Written as `region.UTC()` the choice is visible at the
// call site; written as an omitted argument it would not be.
func UTC() Zone {
	return Zone{name: DefaultTimezone, loc: time.UTC}
}

// Name returns the IANA identifier the zone resolved from.
func (z Zone) Name() string {
	if z.name == "" {
		return DefaultTimezone
	}
	return z.name
}

// Location returns the resolved [time.Location].
//
// Reach for it only when handing a zone to an API that predates [Zone]
// and takes a location — [IsWorkingDay] and [NextWorkingDay] in this
// package do. Day arithmetic goes through [Day] instead, which is
// why nothing here needs to construct a [time.Time] from a location by
// hand.
func (z Zone) Location() *time.Location {
	if z.loc == nil {
		return time.UTC
	}
	return z.loc
}

// IsZero reports whether z is the zero value, which names no zone.
func (z Zone) IsZero() bool { return z.loc == nil }

// String renders the zone as its IANA identifier.
func (z Zone) String() string { return z.Name() }
