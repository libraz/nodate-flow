// Package holidays answers "is this day a public holiday in country X"
// for the server side of the product.
//
// The browser gets the same answer from the `@nodate-flow/holidays`
// TypeScript provider, but due-date snapping happens in the api, where
// that provider cannot run. Keeping a second, hand-maintained holiday
// table in Go would guarantee the two eventually disagree — a task the
// UI shows as landing on a working day would be snapped by the server,
// or the reverse. So data.json is projected from the very same provider
// by scripts/gen-holidays.ts and embedded here; regenerate it with
// `make gen-holidays`.
//
// Only dates are carried. The server never displays a holiday, so names,
// localisations, and the observance/optional classifications the UI
// distinguishes are all dropped: what remains is the set of days that are
// not working days.
package holidays

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

//go:embed data.json
var rawData []byte

// dataset is the shape scripts/gen-holidays.ts emits.
type dataset struct {
	FromYear  int                 `json:"fromYear"`
	ToYear    int                 `json:"toYear"`
	Countries map[string][]string `json:"countries"`
}

var (
	once   sync.Once
	loaded dataset
	index  map[string]map[string]struct{}
)

func load() {
	once.Do(func() {
		if err := json.Unmarshal(rawData, &loaded); err != nil {
			// The file is embedded at build time and produced by a
			// generator that validates its own output, so a parse
			// failure here means the binary itself is malformed.
			panic(fmt.Sprintf("holidays: embedded data.json is unparseable: %v", err))
		}
		index = make(map[string]map[string]struct{}, len(loaded.Countries))
		for code, dates := range loaded.Countries {
			set := make(map[string]struct{}, len(dates))
			for _, d := range dates {
				set[d] = struct{}{}
			}
			index[strings.ToUpper(code)] = set
		}
	})
}

// CoveredYears returns the inclusive year range the embedded dataset
// spans. Dates outside it are reported as non-holidays, which silently
// turns holiday-aware snapping back into weekend-only snapping — the
// package's own test fails once the range stops covering the present, so
// the expiry surfaces in CI rather than in a user's calendar.
func CoveredYears() (from, to int) {
	load()
	return loaded.FromYear, loaded.ToYear
}

// Supported reports whether holiday data ships for the ISO 3166-1
// alpha-2 country code.
func Supported(country string) bool {
	load()
	_, ok := index[strings.ToUpper(country)]
	return ok
}

// Countries returns the country codes the dataset covers, unsorted.
func Countries() []string {
	load()
	out := make([]string, 0, len(index))
	for code := range index {
		out = append(out, code)
	}
	return out
}

// Set returns every public-holiday date for the country as YYYY-MM-DD
// strings, for the whole covered range. The result is the shape the snap
// engine consumes; it is shared, so callers must not mutate it.
//
// Returning the whole range rather than a window is deliberate: a
// country contributes a couple of hundred dates over the covered span,
// which is cheaper to hand over than the bookkeeping a window would need
// to stay correct as a task's due date is moved around.
func Set(country string) map[string]struct{} {
	load()
	if set, ok := index[strings.ToUpper(country)]; ok {
		return set
	}
	return nil
}

// IsHoliday reports whether the given calendar day is a public holiday
// in the country. The day is taken from t as-is, so callers pass a time
// already expressed in the zone whose calendar they mean.
func IsHoliday(country string, t time.Time) bool {
	set := Set(country)
	if set == nil {
		return false
	}
	_, ok := set[t.Format("2006-01-02")]
	return ok
}
