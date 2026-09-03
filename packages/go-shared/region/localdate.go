package region

import (
	"sync"
	"time"
)

// LoadLocation resolves an IANA timezone name to a [time.Location],
// memoising the result.
//
// It is the one place in the non-test tree that calls
// [time.LoadLocation]; [TestZoneResolutionIsCentralised] refuses a
// second. Callers reach it through [Resolve], which applies the fallback
// chain first — this function on its own has no opinion about an empty
// name beyond rejecting it.
//
// [time.LoadLocation] reads the zoneinfo database on every call, and the
// callers here are loops: the drift reconciler resolves a zone per row
// on every pass, and every reschedule resolves one per write. Both
// failures and successes are cached, because an unresolvable name means
// a broken stored row, and a broken row is exactly what a loop revisits
// on a schedule.
func LoadLocation(tz string) (*time.Location, error) {
	if v, ok := locationCache.Load(tz); ok {
		e := v.(locationEntry)
		return e.loc, e.err
	}
	loc, err := time.LoadLocation(tz)
	if tz == "" {
		// time.LoadLocation("") answers UTC. Callers are meant to run the
		// name through Resolve first, so an empty string here is a caller
		// that skipped the chain rather than a user who chose UTC —
		// reject it instead of guessing on their behalf.
		loc, err = nil, ErrInvalidTimezone
	}
	locationCache.Store(tz, locationEntry{loc: loc, err: err})
	return loc, err
}

type locationEntry struct {
	loc *time.Location
	err error
}

var locationCache sync.Map
