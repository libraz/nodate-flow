package authn

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemorySingleUseStoreClaimsOnce(t *testing.T) {
	t.Parallel()
	s := NewMemorySingleUseStore()
	ctx := context.Background()

	first, err := s.Consume(ctx, "token-a", time.Minute)
	if err != nil || !first {
		t.Fatalf("first Consume = (%v, %v), want (true, nil)", first, err)
	}
	second, err := s.Consume(ctx, "token-a", time.Minute)
	if err != nil || second {
		t.Fatalf("second Consume = (%v, %v), want (false, nil)", second, err)
	}
	other, err := s.Consume(ctx, "token-b", time.Minute)
	if err != nil || !other {
		t.Fatalf("Consume of a different id = (%v, %v), want (true, nil)", other, err)
	}
}

func TestMemorySingleUseStoreElectsOneWinnerUnderRace(t *testing.T) {
	t.Parallel()
	s := NewMemorySingleUseStore()
	ctx := context.Background()

	const goroutines = 32
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	results := make([]bool, goroutines)
	for i := range results {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			ok, err := s.Consume(ctx, "contended", time.Minute)
			if err != nil {
				t.Errorf("Consume: %v", err)
			}
			results[i] = ok
		}(i)
	}
	start.Done()
	done.Wait()

	winners := 0
	for _, ok := range results {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d concurrent callers claimed the same id, want exactly 1", winners)
	}
}

func TestMemorySingleUseStoreForgetsExpiredRecords(t *testing.T) {
	t.Parallel()
	s := NewMemorySingleUseStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	ctx := context.Background()

	if ok, _ := s.Consume(ctx, "short-lived", time.Minute); !ok {
		t.Fatal("first Consume must succeed")
	}
	// Once the token itself has expired the record is dead weight: the
	// verifier rejects the token on its own, and keeping the entry would
	// grow the map without bound.
	now = now.Add(2 * time.Minute)
	if ok, _ := s.Consume(ctx, "short-lived", time.Minute); !ok {
		t.Fatal("a record past its ttl must be swept, not remembered forever")
	}
	if len(s.expiries) != 1 {
		t.Fatalf("store holds %d records, want the swept map to keep only the live one", len(s.expiries))
	}
}

func TestMemorySingleUseStoreRejectsUnusableInput(t *testing.T) {
	t.Parallel()
	s := NewMemorySingleUseStore()
	ctx := context.Background()

	if ok, _ := s.Consume(ctx, "", time.Minute); ok {
		t.Error("an empty id must not be claimable")
	}
	// A non-positive ttl means the caller's token has already expired,
	// so recording it would be pointless and claiming it misleading.
	if ok, _ := s.Consume(ctx, "expired", 0); ok {
		t.Error("a zero ttl must not be claimable")
	}
	if ok, _ := s.Consume(ctx, "expired", -time.Second); ok {
		t.Error("a negative ttl must not be claimable")
	}
}
