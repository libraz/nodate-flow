package agentruntime

import (
	"context"
	"database/sql"
	"time"
)

// DBSource is a [Source] backed by raw SQL against the ai_agents
// table. It returns every agent with schedule_kind = 'interval' that
// is not paused. The scheduler fires at a fixed interval configured
// via NF_AGENT_TICK_INTERVAL, so there is no per-row cron evaluation.
type DBSource struct {
	DB *sql.DB
}

const dueAgentsQuery = `
SELECT id, workspace_id, paused
FROM ai_agents
WHERE enabled = TRUE
  AND schedule_kind = 'interval'
  AND paused = FALSE
`

// Due implements [Source]. Every non-paused interval-scheduled agent
// is considered due on every tick — rate control is global, not
// per-row, which matches the "one tick interval for the whole
// process" design.
func (s *DBSource) Due(ctx context.Context, _ time.Time) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, dueAgentsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Job, 0)
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.AgentID, &j.WsID, &j.Paused); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// LogRunner is a [Runner] that records each fired job to a sink
// function. It exists so the api can wire the scheduler at startup
// without depending on a real LLM client; production wiring will
// swap this for an orchestrator-backed runner.
type LogRunner struct {
	Sink func(ctx context.Context, j Job, now time.Time)
}

// Run implements [Runner].
func (l *LogRunner) Run(ctx context.Context, j Job, now time.Time) error {
	if l.Sink != nil {
		l.Sink(ctx, j, now)
	}
	return nil
}
