package tasks

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// TestRetroDraftsCostDoesNotGrowWithThePage is the behavioural guard on
// the retro draft queue.
//
// Each draft carries two optional strings — the name of the agent that
// wrote it and that agent's id — and they come from the task's
// task.retro.drafted event rather than from the tasks row. Resolving
// them one task at a time meant a page of fifty drafts cost fifty-one
// statements, and the cost grew with the page the caller asked for: the
// one number a list endpoint must keep flat.
//
// The assertion is the shape rather than a timing: five drafts and three
// hundred must cost the same number of statements. That is what a
// per-row lookup cannot satisfy however it is written — inside a loop,
// inside a helper, or behind a condition — because the count is measured
// at the driver.
func TestRetroDraftsCostDoesNotGrowWithThePage(t *testing.T) {
	t.Parallel()

	small := retroDraftStatementCount(t, 5)
	large := retroDraftStatementCount(t, 300)

	require.Equal(t, small, large,
		"listing retro drafts must cost the same for 5 drafts and for 300; "+
			"a per-draft agent lookup makes the page cost scale with the page size")
	require.Equal(t, 2, large,
		"the page is one list read plus one batched agent read; got %d statements", large)
}

// TestRetroDraftsStillAttributeEveryAgent is the correctness half: the
// batched lookup must put the right agent on the right draft, not merely
// issue fewer statements. The stub gives every task its own agent name,
// so an implementation that indexes the batch result by position instead
// of by task id — or that drops rows whose event is missing — is visible
// in the response.
func TestRetroDraftsStillAttributeEveryAgent(t *testing.T) {
	t.Parallel()

	stub := &draftStub{drafts: 4, agentForTask: map[int64]string{1: "Scribe", 3: "Archivist"}}
	db := openDraftStubDB(t, stub)
	defer db.Close()

	out, err := listRetroDrafts(context.Background(), generated.New(db), 1, elevatedDraftReader, 50, 0)
	require.NoError(t, err)
	require.Len(t, out.Body.Drafts, 4)

	// Tasks 1 and 3 have a drafted event; 2 and 4 do not, and their
	// optional fields stay empty rather than borrowing a neighbour's.
	require.Equal(t, "Scribe", out.Body.Drafts[0].CreatedByAgentName)
	require.Empty(t, out.Body.Drafts[1].CreatedByAgentName)
	require.Equal(t, "Archivist", out.Body.Drafts[2].CreatedByAgentName)
	require.Empty(t, out.Body.Drafts[3].CreatedByAgentName)

	require.NotEmpty(t, out.Body.Drafts[0].CreatedByAgentID)
	require.Empty(t, out.Body.Drafts[1].CreatedByAgentID)
}

// retroDraftStatementCount lists one page of n drafts and returns how
// many statements it cost.
func retroDraftStatementCount(t *testing.T, n int) int {
	t.Helper()

	// Every task gets an agent, so a per-row lookup finds a row each time
	// and completes — the test then fails on the statement count, which
	// is the defect, rather than on an error the stub invented.
	stub := &draftStub{drafts: n}
	db := openDraftStubDB(t, stub)
	defer db.Close()

	out, err := listRetroDrafts(context.Background(), generated.New(db), 1, elevatedDraftReader, int32(n), 0) //#nosec G115 -- n is a test constant
	require.NoError(t, err)
	require.Len(t, out.Body.Drafts, n)

	return stub.count()
}

// elevatedDraftReader is the visibility binding these tests read the page
// with. The stub driver answers with a fixed row set whatever the binds
// are, so the value only has to be well-formed; what is under test is the
// number of statements and the agent attribution, not the filter.
var elevatedDraftReader = acl.ListVisibilityArgs(1, acl.WorkspaceRoleOwner)

// --- stub driver ------------------------------------------------------

type draftStub struct {
	drafts int
	// agentForTask names the agent behind each task id. Nil means every
	// task has one, which is what the statement-count runs want.
	agentForTask map[int64]string

	mu         sync.Mutex
	statements []string
}

func (s *draftStub) record(q string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements = append(s.statements, q)
}

func (s *draftStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.statements)
}

func (s *draftStub) agentName(taskID int64) (string, bool) {
	if s.agentForTask == nil {
		return "Agent", true
	}
	name, ok := s.agentForTask[taskID]
	return name, ok
}

func (s *draftStub) Open(string) (driver.Conn, error) { return &draftConn{s: s}, nil }

type draftConn struct{ s *draftStub }

func (c *draftConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *draftConn) Close() error                        { return nil }
func (c *draftConn) Begin() (driver.Tx, error)           { return draftTx{}, nil }

type draftTx struct{}

func (draftTx) Commit() error   { return nil }
func (draftTx) Rollback() error { return nil }

func (c *draftConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.s.record(query)
	switch {
	case strings.Contains(query, "ROW_NUMBER() OVER"):
		return c.s.agentRows(args), nil
	case strings.Contains(query, "retro_of"):
		return c.s.draftRows(), nil
	default:
		return &draftRows{}, nil
	}
}

func (s *draftStub) draftRows() driver.Rows {
	return &draftRows{
		cols: []string{
			"task_id", "task_public_id", "title", "description", "created_at",
			"source_task_public_id", "source_task_title", "total",
		},
		n: s.drafts,
		row: func(i int) []driver.Value {
			task := types.New()
			src := types.New()
			return []driver.Value{
				int64(i),
				append([]byte(nil), task[:]...),
				"Retro draft",
				nil,
				time.Now(),
				append([]byte(nil), src[:]...),
				"Source task",
				int64(s.drafts),
			}
		},
	}
}

// agentRows answers FindRetroDraftAgents. The task ids arrive after the
// workspace id, so the reply is built from exactly what was asked for —
// which is what makes the row-per-task shape observable: a batched call
// asks for the whole page and a per-row call asks for one id at a time,
// and both are answered correctly.
func (s *draftStub) agentRows(args []driver.NamedValue) driver.Rows {
	type agent struct {
		taskID int64
		name   string
	}
	var wanted []agent
	for _, a := range args[1:] {
		id, ok := a.Value.(int64)
		if !ok {
			continue
		}
		if name, has := s.agentName(id); has {
			wanted = append(wanted, agent{taskID: id, name: name})
		}
	}
	return &draftRows{
		cols: []string{"task_id", "agent_public_id", "agent_name"},
		n:    len(wanted),
		row: func(i int) []driver.Value {
			pub := types.New()
			return []driver.Value{
				wanted[i-1].taskID,
				append([]byte(nil), pub[:]...),
				wanted[i-1].name,
			}
		},
	}
}

type draftRows struct {
	cols []string
	row  func(i int) []driver.Value
	n    int
	i    int
}

func (r *draftRows) Columns() []string { return r.cols }
func (r *draftRows) Close() error      { return nil }

func (r *draftRows) Next(dest []driver.Value) error {
	if r.i >= r.n {
		return io.EOF
	}
	r.i++
	copy(dest, r.row(r.i))
	return nil
}

var draftStubSeq atomic.Uint64

func openDraftStubDB(t *testing.T, s *draftStub) *sql.DB {
	t.Helper()

	// database/sql keeps a process-wide driver registry, so each test
	// needs its own name to stay parallel-safe.
	name := "tasks-drafts-stub-" + time.Now().Format("150405.000000000") + "-" +
		string(rune('a'+draftStubSeq.Add(1)%26))
	sql.Register(name, s)
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	return db
}
