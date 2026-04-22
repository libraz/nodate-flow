// Package region provides shared helpers for timezone / country handling.
//
// Users and workspaces both store a preferred IANA timezone and an optional
// ISO 3166-1 alpha-2 country code. These two axes are independent of the
// BCP 47 locale tag: a Japanese-speaker living in Germany can have
// locale="ja", timezone="Europe/Berlin", country="DE".
//
// Effective resolution order (least specific wins if NULL):
//
//	explicit value on row > user.timezone > workspace.timezone > "UTC"
//	explicit value on row > user.country  > workspace.country  > ""
package region

import (
	"regexp"
	"time"
)

// DefaultTimezone is the fallback used when no tz is set anywhere in the chain.
const DefaultTimezone = "UTC"

var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)

// ValidateTimezone returns nil when tz is a valid IANA timezone identifier
// that time.LoadLocation can resolve. Empty string is rejected so callers
// must pass DefaultTimezone explicitly.
func ValidateTimezone(tz string) error {
	if tz == "" {
		return ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return ErrInvalidTimezone
	}
	return nil
}

// ValidateCountry returns nil when cc is a two-letter uppercase country code
// that matches ISO 3166-1 alpha-2. Empty string is allowed (meaning "unset").
func ValidateCountry(cc string) error {
	if cc == "" {
		return nil
	}
	if !countryCodePattern.MatchString(cc) {
		return ErrInvalidCountry
	}
	if _, ok := supportedCountries[cc]; !ok {
		return ErrInvalidCountry
	}
	return nil
}

// EffectiveTimezone resolves the timezone to use for a given context. The
// first non-empty value in the chain wins; if all are empty, DefaultTimezone
// is returned. Callers should pass in priority order (most specific first).
func EffectiveTimezone(candidates ...string) string {
	for _, tz := range candidates {
		if tz != "" {
			return tz
		}
	}
	return DefaultTimezone
}

// EffectiveCountry resolves the country code to use for a given context. The
// first non-empty value in the chain wins; if all are empty, an empty string
// is returned (callers treat this as "no holiday subscription by default").
func EffectiveCountry(candidates ...string) string {
	for _, cc := range candidates {
		if cc != "" {
			return cc
		}
	}
	return ""
}

// SupportedCountries returns the set of ISO 3166-1 alpha-2 codes that the
// system currently ships holiday data for. The returned map is safe to read
// but must not be mutated.
func SupportedCountries() map[string]string {
	return supportedCountries
}

// supportedCountries maps ISO 3166-1 alpha-2 → English display name. The
// list is intentionally limited to regions for which the holidays package
// ships data; extending this list requires a corresponding provider entry.
var supportedCountries = map[string]string{
	"JP": "Japan",
	"US": "United States",
	"GB": "United Kingdom",
	"DE": "Germany",
	"FR": "France",
	"IT": "Italy",
	"ES": "Spain",
	"CA": "Canada",
	"AU": "Australia",
	"NZ": "New Zealand",
	"KR": "South Korea",
	"CN": "China",
	"TW": "Taiwan",
	"HK": "Hong Kong",
	"SG": "Singapore",
	"IN": "India",
	"BR": "Brazil",
	"MX": "Mexico",
	"NL": "Netherlands",
	"SE": "Sweden",
	"NO": "Norway",
	"FI": "Finland",
	"DK": "Denmark",
	"CH": "Switzerland",
	"AT": "Austria",
	"BE": "Belgium",
	"IE": "Ireland",
	"PT": "Portugal",
	"PL": "Poland",
	"CZ": "Czech Republic",
	"TH": "Thailand",
	"VN": "Vietnam",
	"PH": "Philippines",
	"ID": "Indonesia",
	"MY": "Malaysia",
	"AE": "United Arab Emirates",
	"SA": "Saudi Arabia",
	"IL": "Israel",
	"TR": "Turkey",
	"RU": "Russia",
	"ZA": "South Africa",
	"AR": "Argentina",
	"CL": "Chile",
	"CO": "Colombia",
}
