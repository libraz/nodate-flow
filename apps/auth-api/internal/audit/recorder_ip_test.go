package audit

import (
	"context"
	"database/sql"
	"net"
	"testing"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// captureResult is a no-op sql.Result for the fake DBTX.
type captureResult struct{}

func (captureResult) LastInsertId() (int64, error) { return 1, nil }
func (captureResult) RowsAffected() (int64, error) { return 1, nil }

// captureDBTX records the positional args of the most recent ExecContext
// so tests can assert what the recorder wrote without a real database.
type captureDBTX struct {
	lastArgs []interface{}
}

func (c *captureDBTX) ExecContext(_ context.Context, _ string, args ...interface{}) (sql.Result, error) {
	c.lastArgs = args
	return captureResult{}, nil
}

func (c *captureDBTX) PrepareContext(context.Context, string) (*sql.Stmt, error) { return nil, nil }
func (c *captureDBTX) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, nil
}
func (c *captureDBTX) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return nil
}

func argNullString(t *testing.T, args []interface{}, i int) sql.NullString {
	t.Helper()
	if i >= len(args) {
		t.Fatalf("arg index %d out of range (len=%d)", i, len(args))
	}
	v, ok := args[i].(sql.NullString)
	if !ok {
		t.Fatalf("arg %d is %T, want sql.NullString", i, args[i])
	}
	return v
}

// TestRecord_LoginCarriesIPAndUserAgent asserts that an instance-scoped
// login audit row written by the recorder carries a non-null packed IP
// and a User-Agent pulled from the request context.
func TestRecord_LoginCarriesIPAndUserAgent(t *testing.T) {
	fake := &captureDBTX{}
	rec := New(generated.New(fake))

	ctx := authn.WithClientIP(context.Background(), "203.0.113.7")
	ctx = authn.WithUserAgent(ctx, "auth-e2e/1.0")

	rec.Record(ctx, Entry{
		Action: "auth.login",
		// WorkspaceID == 0 routes to instance_audit_logs.
	})

	// instance_audit_logs columns: public_id, actor_user_id, action,
	// target_workspace_id, target_resource_type, target_resource_public_id,
	// ip_address, user_agent, payload_json, occurred_at.
	ip := argNullString(t, fake.lastArgs, 6)
	if !ip.Valid {
		t.Fatal("ip_address is NULL, want non-null packed IP")
	}
	if want := string(net.ParseIP("203.0.113.7").To16()); ip.String != want {
		t.Fatalf("ip_address = %q, want packed %q", ip.String, want)
	}
	ua := argNullString(t, fake.lastArgs, 7)
	if !ua.Valid || ua.String != "auth-e2e/1.0" {
		t.Fatalf("user_agent = %+v, want valid 'auth-e2e/1.0'", ua)
	}
}

// TestRecord_WorkspaceScopedCarriesIPAndUserAgent asserts that a
// workspace-scoped audit row (e.g. a member change) also carries the IP
// and User-Agent.
func TestRecord_WorkspaceScopedCarriesIPAndUserAgent(t *testing.T) {
	fake := &captureDBTX{}
	rec := New(generated.New(fake))

	ctx := authn.WithClientIP(context.Background(), "198.51.100.23")
	ctx = authn.WithUserAgent(ctx, "member-agent")

	rec.Record(ctx, Entry{
		Action:       "workspace.member.add",
		ActorID:      7,
		WorkspaceID:  3,
		ResourceType: "membership",
	})

	// audit_logs columns: public_id, workspace_id, actor_user_id, action,
	// resource_type, resource_public_id, ip_address, user_agent,
	// metadata_json, occurred_at.
	ip := argNullString(t, fake.lastArgs, 6)
	if !ip.Valid || len(ip.String) != net.IPv6len {
		t.Fatalf("ip_address invalid: valid=%v len=%d", ip.Valid, len(ip.String))
	}
	ua := argNullString(t, fake.lastArgs, 7)
	if !ua.Valid || ua.String != "member-agent" {
		t.Fatalf("user_agent = %+v, want valid 'member-agent'", ua)
	}
}

// TestPackIP covers the IP normalization edge cases.
func TestPackIP(t *testing.T) {
	if got := packIP(""); got.Valid {
		t.Error("empty IP should yield NULL")
	}
	if got := packIP("bogus"); got.Valid {
		t.Error("unparseable IP should yield NULL")
	}
	if got := packIP("2001:db8::1"); !got.Valid || len(got.String) != net.IPv6len {
		t.Errorf("IPv6 should pack to 16 bytes, got valid=%v len=%d", got.Valid, len(got.String))
	}
	if got := packIP("10.0.0.1"); !got.Valid || len(got.String) != net.IPv6len {
		t.Errorf("IPv4 should pack to 16 bytes, got valid=%v len=%d", got.Valid, len(got.String))
	}
}
