package brand

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAppName(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"empty", "", DefaultAppName, false},
		{"whitespace", " \t ", DefaultAppName, false},
		{"trimmed", "  Acme  ", "Acme", false},
		{"64 ascii", strings.Repeat("a", 64), strings.Repeat("a", 64), false},
		{"64 runes with astral", strings.Repeat("a", 63) + "😀", strings.Repeat("a", 63) + "😀", false},
		{"65 runes", strings.Repeat("a", 65), "", true},
		{"65 runes with astral", strings.Repeat("a", 64) + "😀", "", true},
		{"newline inside", "a\nb", "", true},
		{"carriage return inside", "a\rb", "", true},
		{"nul", "a\x00b", "", true},
		{"del", "a\x7fb", "", true},
		{"C1 NEL", "a\u0085b", "", true},
		{"line separator", "a\u2028b", "", true},
		{"paragraph separator", "a\u2029b", "", true},
		{"bidi override", "a\u202eb", "", true},
		{"bidi isolate", "a\u2066b", "", true},
		{"colon", "a:b", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAppName(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "NF_APP_NAME")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveAppNameErrorOmitsValue(t *testing.T) {
	_, err := ResolveAppName("secret:value")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret")
}

func TestNameOrDefault(t *testing.T) {
	assert.Equal(t, DefaultAppName, NameOrDefault(""))
	assert.Equal(t, "Acme", NameOrDefault("Acme"))
}

func TestResolveImageURL(t *testing.T) {
	const kindErr = "must be an https URL or a data:image/(png|svg+xml|webp|jpeg|gif) URI"
	const asciiErr = "must contain only printable ASCII characters"
	dataOf := func(n int) string {
		p := "data:image/png;base64,"
		return p + strings.Repeat("A", n-len(p))
	}
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{"empty", "", "", ""},
		{"spaces", "   ", "", ""},
		{"tab newline", "\t\n", "", ""},
		{"trimmed", " https://cdn.example/l.svg ", "https://cdn.example/l.svg", ""},
		{"upper scheme", "HTTPS://cdn.example/l.svg", "HTTPS://cdn.example/l.svg", ""},
		{"upper data", "DATA:image/png;base64,AAAA", "DATA:image/png;base64,AAAA", ""},
		{"svg data", "data:image/svg+xml,%3Csvg%3E", "data:image/svg+xml,%3Csvg%3E", ""},
		{"bmp", "data:image/bmp;base64,AAAA", "", kindErr},
		{"http", "http://cdn.example/l.svg", "", kindErr},
		{"no host", "https://", "", kindErr},
		{"no scheme", "cdn.example/l.svg", "", kindErr},
		{"inner space", "https://cdn.example/a b.svg", "", asciiErr},
		{"inner newline", "https://a.example/x\ny", "", asciiErr},
		{"non-ascii", "https://例え.jp/l.svg", "", asciiErr},
		{"data 32768", dataOf(32768), dataOf(32768), ""},
		{"data 32769", dataOf(32769), "", "data URI must be at most 32768 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveImageURL("NF_APP_LOGO_URL", tt.raw)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "NF_APP_LOGO_URL")
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsDataURI(t *testing.T) {
	assert.True(t, IsDataURI("data:image/png,"))
	assert.True(t, IsDataURI("DATA:x"))
	assert.False(t, IsDataURI("https://x"))
	assert.False(t, IsDataURI("dat"))
}
