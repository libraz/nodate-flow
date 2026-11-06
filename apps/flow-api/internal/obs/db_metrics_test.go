package obs

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// The shutdown's metrics phase stops the collector and then waits for it,
// so the handle has to report the loop's actual return.
func TestDBStatsCollectorStopsAndCanBeWaited(t *testing.T) {
	h := StartDBStatsCollector(context.Background(), &sql.DB{})
	h.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Wait(ctx); err != nil {
		t.Fatalf("Wait after Stop: %v", err)
	}
}
