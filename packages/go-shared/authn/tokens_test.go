package authn

import "testing"

func TestRandomHexLength(t *testing.T) {
	t.Parallel()
	for _, n := range []int{8, 16, 32} {
		h := RandomHex(n)
		if len(h) != n*2 {
			t.Errorf("RandomHex(%d) = %q (len %d), want len %d", n, h, len(h), n*2)
		}
	}
}

func TestRandomHexUniqueness(t *testing.T) {
	t.Parallel()
	a := RandomHex(16)
	b := RandomHex(16)
	if a == b {
		t.Errorf("two RandomHex calls returned same value: %q", a)
	}
}
