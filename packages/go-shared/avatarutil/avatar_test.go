package avatarutil

import (
	"slices"
	"strings"
	"testing"
)

func TestURLForClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		stored        string
		userPublicID  string
		publicBaseURL string
		want          string
	}{
		{
			name:          "empty stored returns empty",
			stored:        "",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "https://auth.example.com",
			want:          "",
		},
		{
			name:          "https URL passed through verbatim",
			stored:        "https://lh3.googleusercontent.com/a/AcjK=s96-c",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "https://auth.example.com",
			want:          "https://lh3.googleusercontent.com/a/AcjK=s96-c",
		},
		{
			name:          "http URL passed through verbatim",
			stored:        "http://insecure.example/avatar.jpg",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "https://auth.example.com",
			want:          "http://insecure.example/avatar.jpg",
		},
		{
			name:          "uppercase HTTPS scheme still passed through",
			stored:        "HTTPS://CDN.EXAMPLE/A.JPG",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "https://auth.example.com",
			want:          "HTTPS://CDN.EXAMPLE/A.JPG",
		},
		{
			name:          "storage key rewritten to proxy URL with cache-buster",
			stored:        "avatars/01900000-0000-7000-8000-000000000001/01910000-0000-7000-8000-000000000abc.jpg",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "https://auth.example.com",
			want:          "https://auth.example.com/avatars/01900000-0000-7000-8000-000000000001?v=01910000",
		},
		{
			name:          "trailing slash on base is trimmed",
			stored:        "avatars/u/01910000-aaaa-7000-8000-000000000abc.png",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "https://auth.example.com/",
			want:          "https://auth.example.com/avatars/01900000-0000-7000-8000-000000000001?v=01910000",
		},
		{
			name:          "empty publicBaseURL produces relative URL",
			stored:        "avatars/u/01910000-0000-7000-8000-000000000abc.jpg",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "",
			want:          "/avatars/01900000-0000-7000-8000-000000000001?v=01910000",
		},
		{
			name:          "storage key without extension still produces token",
			stored:        "avatars/u/0191aaaa00007000800000000000babe",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "https://auth.example.com",
			want:          "https://auth.example.com/avatars/01900000-0000-7000-8000-000000000001?v=0191aaaa",
		},
		{
			name:          "storage key with short filename falls back to that filename",
			stored:        "avatars/u/abc.jpg",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "https://auth.example.com",
			want:          "https://auth.example.com/avatars/01900000-0000-7000-8000-000000000001?v=abc",
		},
		{
			// Files whose name is a leading dot keep the extension as the
			// cache-buster — LastIndex returns 0 so the strip is skipped
			// to preserve dotfile-style names. Verifies the documented
			// edge case rather than the prettier-but-incorrect "0" path.
			name:          "leading-dot filename keeps the extension as the token",
			stored:        "avatars/u/.jpg",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			publicBaseURL: "https://auth.example.com",
			want:          "https://auth.example.com/avatars/01900000-0000-7000-8000-000000000001?v=.jpg",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := URLForClient(tc.stored, tc.userPublicID, tc.publicBaseURL)
			if got != tc.want {
				t.Fatalf("URLForClient(%q, %q, %q) = %q; want %q",
					tc.stored, tc.userPublicID, tc.publicBaseURL, got, tc.want)
			}
		})
	}
}

// TestOpaqueTag_HidesTheSequence is the point of the helper: the tag must
// not contain, and must not order by, the row id it was derived from.
// Consecutive ids previously produced consecutive "?v=" values, which put
// the instance's storage_objects sequence in a URL handed to the browser.
func TestOpaqueTag_HidesTheSequence(t *testing.T) {
	t.Parallel()

	const id = 48213
	tag := OpaqueTag(id)

	if strings.Contains(tag, "48213") {
		t.Fatalf("OpaqueTag(%d) = %q; the row id is still readable", id, tag)
	}
	if len(tag) != 8 {
		t.Fatalf("OpaqueTag(%d) = %q; want 8 hex characters", id, tag)
	}
	for _, r := range tag {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("OpaqueTag(%d) = %q; non-hex character %q", id, tag, r)
		}
	}

	// A run of consecutive ids must not come out sorted, or an observer
	// could still rank users by upload order. Three samples would agree
	// by chance one time in six, so the run is long enough that agreement
	// cannot be a coincidence; the digest is fixed, so the outcome is
	// deterministic.
	tags := make([]string, 0, 20)
	for i := uint64(1); i <= 20; i++ {
		tags = append(tags, OpaqueTag(i))
	}
	if slices.IsSorted(tags) {
		t.Fatalf("OpaqueTag preserved the ordering of consecutive ids: %q", tags)
	}
	if len(slices.Compact(slices.Clone(tags))) != len(tags) {
		t.Fatalf("OpaqueTag produced a duplicate across consecutive ids: %q", tags)
	}
}

// TestOpaqueTag_IsStable pins the cache-buster contract: the same id must
// keep producing the same tag, or every profile read would bust the
// browser cache.
func TestOpaqueTag_IsStable(t *testing.T) {
	t.Parallel()

	if a, b := OpaqueTag(7), OpaqueTag(7); a != b {
		t.Fatalf("OpaqueTag(7) is not stable: %q vs %q", a, b)
	}
	if a, b := OpaqueTag(7), OpaqueTag(8); a == b {
		t.Fatalf("OpaqueTag does not change with the id: %q", a)
	}
}

// TestProxyURL covers the shape the /me handler now builds directly.
func TestProxyURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		userPublicID  string
		tag           string
		publicBaseURL string
		want          string
	}{
		{
			name:          "absolute base",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			tag:           "deadbeef",
			publicBaseURL: "https://auth.example.com",
			want:          "https://auth.example.com/avatars/01900000-0000-7000-8000-000000000001?v=deadbeef",
		},
		{
			name:          "trailing slash trimmed",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			tag:           "deadbeef",
			publicBaseURL: "https://auth.example.com/",
			want:          "https://auth.example.com/avatars/01900000-0000-7000-8000-000000000001?v=deadbeef",
		},
		{
			name:          "empty base yields a relative URL",
			userPublicID:  "01900000-0000-7000-8000-000000000001",
			tag:           "deadbeef",
			publicBaseURL: "",
			want:          "/avatars/01900000-0000-7000-8000-000000000001?v=deadbeef",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ProxyURL(tc.userPublicID, tc.tag, tc.publicBaseURL); got != tc.want {
				t.Fatalf("ProxyURL(%q, %q, %q) = %q; want %q",
					tc.userPublicID, tc.tag, tc.publicBaseURL, got, tc.want)
			}
		})
	}
}
