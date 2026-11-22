package asanaimport

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLookupHelpers(t *testing.T) {
	t.Run("chunks", func(t *testing.T) {
		for _, c := range []struct{ n, chunks, last int }{
			{0, 0, 0}, {1, 1, 1}, {lookupChunk, 1, lookupChunk}, {lookupChunk + 1, 2, 1},
		} {
			got := chunks(make([]uint32, c.n))
			assert.Len(t, got, c.chunks, c.n)
			if c.chunks > 0 {
				assert.Len(t, got[len(got)-1], c.last, c.n)
			}
		}
	})

	t.Run("name keys follow an accent- and case-insensitive collation", func(t *testing.T) {
		for _, c := range []struct {
			a, b string
			same bool
		}{
			{"Bug", "bug", true},
			{"Café", "cafe", true},
			{"Bug", "Bugs", false},
		} {
			assert.Equal(t, c.same, nameKey(c.a) == nameKey(c.b), c.a+"/"+c.b)
		}
	})

	assert.Equal(t, "asana:42", SourceUID("42"))
}
