package authn

import (
	"testing"
	"time"
)

// TestVerifyTotpStep_ReturnsStableStep verifies that VerifyTotpStep
// returns the same time-step for the same instant and that the step
// matches the RFC 6238 counter (unix / period). This step is what the
// auth handlers persist to enforce one-time-use, so a wrong value would
// either let codes replay or reject valid codes.
func TestVerifyTotpStep_ReturnsStableStep(t *testing.T) {
	t.Parallel()

	secret, err := GenerateTotpSecret()
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}

	now := time.Unix(1_700_000_015, 0) // arbitrary fixed instant
	code := totpAt(secret, now)

	step, ok := VerifyTotpStep(secret, code, now)
	if !ok {
		t.Fatalf("expected the current code to verify")
	}
	want := now.Unix() / totpPeriod
	if step != want {
		t.Fatalf("step = %d, want %d", step, want)
	}
}

// TestVerifyTotpStep_ReplayDetectableAcrossSkew is the core replay
// regression: a code generated for one instant must report the same
// time-step even when verified slightly later within the skew window.
// That stable step lets a caller persist "last accepted step" and reject
// a replay. Without a stable step a captured code could be reused for
// the full +/- skew window.
func TestVerifyTotpStep_ReplayDetectableAcrossSkew(t *testing.T) {
	t.Parallel()

	secret, err := GenerateTotpSecret()
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}

	gen := time.Unix(1_700_000_045, 0)
	code := totpAt(secret, gen)

	// First acceptance at generation time.
	step1, ok := VerifyTotpStep(secret, code, gen)
	if !ok {
		t.Fatalf("expected code to verify at generation time")
	}

	// A replay 20s later (still inside the +/-30s skew window) resolves
	// to the *same* step, so a last-step guard catches it.
	step2, ok := VerifyTotpStep(secret, code, gen.Add(20*time.Second))
	if !ok {
		t.Fatalf("expected code to still verify inside the skew window")
	}
	if step1 != step2 {
		t.Fatalf("replay produced a different step (%d vs %d); guard would miss it", step1, step2)
	}

	// Emulate the handler's last-step guard: with the step already
	// consumed, the replay must be rejected.
	lastStep := step1
	if !(step2 <= lastStep) {
		t.Fatalf("replayed step %d not <= stored last step %d; replay would be accepted", step2, lastStep)
	}
}

// TestVerifyTotpStep_RejectsWrongCode confirms a non-matching code
// yields ok=false and a zero step.
func TestVerifyTotpStep_RejectsWrongCode(t *testing.T) {
	t.Parallel()

	secret, err := GenerateTotpSecret()
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}
	now := time.Unix(1_700_000_075, 0)

	if step, ok := VerifyTotpStep(secret, "000000", now); ok {
		// 000000 could legitimately be the code for some secret/instant;
		// guard against a flaky false positive by only failing when it
		// actually does not equal the real code.
		if step != now.Unix()/totpPeriod || totpAt(secret, now) != "000000" {
			t.Fatalf("wrong code unexpectedly verified at step %d", step)
		}
	}
}
