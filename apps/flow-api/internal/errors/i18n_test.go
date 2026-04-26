// Hand-written test (not generated). Asserts that every error code defined
// in errors/*.yaml has a non-empty translation in every supported locale
// (en, ja, zh) for both web apps. The generated locale catalogues are the
// authoritative artefact consumed by the frontend, so we test them directly.

package errors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// localesPath returns the absolute path to a locale directory under apps/<app>/locales/<lang>.
// It walks up from the current package directory to the repository root.
func localesPath(t *testing.T, app, lang string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "errors")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "scripts", "gen-errors.go")); err == nil {
				return filepath.Join(dir, "apps", app, "locales", lang, "errors.json")
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate repository root")
	return ""
}

// loadLocale reads a generated errors.json catalogue.
func loadLocale(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

// TestErrorLocaleCoverage asserts that en, ja and zh catalogues for both
// web apps contain identical key sets and that every value is non-empty.
// The single source of truth is errors/*.yaml; gen-errors writes the
// catalogues. Re-run `make gen-errors` if this test fails after a YAML edit.
func TestErrorLocaleCoverage(t *testing.T) {
	apps := []string{"flow-web", "accounts-web"}
	langs := []string{"en", "ja", "zh"}

	for _, app := range apps {
		// Use en as the canonical key set for the app, then verify ja and zh
		// match it exactly.
		canonical := loadLocale(t, localesPath(t, app, "en"))
		if len(canonical) == 0 {
			t.Fatalf("%s/en/errors.json is empty", app)
		}
		for _, lang := range langs {
			path := localesPath(t, app, lang)
			cat := loadLocale(t, path)
			if len(cat) != len(canonical) {
				t.Errorf("%s/%s: expected %d codes, got %d", app, lang, len(canonical), len(cat))
			}
			for code := range canonical {
				val, ok := cat[code]
				if !ok {
					t.Errorf("%s/%s: missing code %q", app, lang, code)
					continue
				}
				if val == "" {
					t.Errorf("%s/%s: empty translation for code %q", app, lang, code)
				}
			}
			for code := range cat {
				if _, ok := canonical[code]; !ok {
					t.Errorf("%s/%s: extra code %q not present in en catalogue", app, lang, code)
				}
			}
		}
	}
}

// TestErrorZHTranslated asserts that no zh entry simply mirrors the English
// message verbatim. gen-errors falls back to English if message_zh is empty
// in the YAML; once every error has a Simplified Chinese translation, no
// such fallback should remain.
//
// If a future error legitimately needs identical text in en and zh
// (extremely unusual for a non-empty user-facing message), exempt it here
// rather than allowing the fallback to silently mask missing translations.
func TestErrorZHTranslated(t *testing.T) {
	for _, app := range []string{"flow-web", "accounts-web"} {
		en := loadLocale(t, localesPath(t, app, "en"))
		zh := loadLocale(t, localesPath(t, app, "zh"))
		for code, enVal := range en {
			zhVal, ok := zh[code]
			if !ok || zhVal == "" {
				// Already covered by TestErrorLocaleCoverage; skip here.
				continue
			}
			if zhVal == enVal {
				t.Errorf("%s: zh translation for %q is identical to en (likely a missing message_zh in errors/*.yaml)", app, code)
			}
		}
	}
}
