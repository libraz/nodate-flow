package avatarutil

import "testing"

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
