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
		{"line separator", "a b", "", true},
		{"paragraph separator", "a b", "", true},
		{"bidi override", "a‮b", "", true},
		{"bidi isolate", "a⁦b", "", true},
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
