package dbtz

import "testing"

func TestNormalizeDSNAddsTheParameter(t *testing.T) {
	t.Parallel()
	got := NormalizeDSN("user:pw@tcp(host:3306)/db")
	want := "user:pw@tcp(host:3306)/db?time_zone=%27%2B00%3A00%27"
	if got != want {
		t.Fatalf("NormalizeDSN = %q, want %q", got, want)
	}
}

func TestNormalizeDSNKeepsOtherParameters(t *testing.T) {
	t.Parallel()
	got := NormalizeDSN("user:pw@tcp(host:3306)/db?parseTime=true&charset=utf8mb4")
	if !contains(got, "parseTime=true") || !contains(got, "charset=utf8mb4") {
		t.Fatalf("NormalizeDSN dropped existing parameters: %q", got)
	}
	if !contains(got, "time_zone=") {
		t.Fatalf("NormalizeDSN did not pin the timezone: %q", got)
	}
}

// A DSN that already names a different zone is a misconfiguration, not a
// preference: the stored values are UTC either way, so honouring it
// would reintroduce the drift.
func TestNormalizeDSNOverridesAnExistingZone(t *testing.T) {
	t.Parallel()
	got := NormalizeDSN("user:pw@tcp(host:3306)/db?time_zone=%27%2B09%3A00%27&parseTime=true")
	if contains(got, "09") {
		t.Fatalf("NormalizeDSN kept a non-UTC zone: %q", got)
	}
	if !contains(got, "parseTime=true") {
		t.Fatalf("NormalizeDSN dropped an unrelated parameter: %q", got)
	}
	if count(got, "time_zone=") != 1 {
		t.Fatalf("NormalizeDSN left more than one timezone parameter: %q", got)
	}
}

func TestNormalizeDSNLeavesAnEmptyStringAlone(t *testing.T) {
	t.Parallel()
	if got := NormalizeDSN(""); got != "" {
		t.Fatalf("NormalizeDSN(\"\") = %q, want empty", got)
	}
}

func contains(haystack, needle string) bool {
	return count(haystack, needle) > 0
}

func count(haystack, needle string) int {
	n := 0
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			n++
		}
	}
	return n
}
