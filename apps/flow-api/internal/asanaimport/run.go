package asanaimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
)

// Exit codes of Run, as the shared importer specification fixes them.
const (
	ExitOK       = 0 // success
	ExitFailed   = 1 // at least one write failed; the rest stays written
	ExitNoWrites = 2 // nothing was written
)

// Options is everything Run reads.
type Options struct {
	// BaseURL is the Asana API root; empty means the public API. Tests only.
	BaseURL string
	// Token is the Asana personal access token (ASANA_TOKEN).
	Token        string
	Mapping      io.Reader
	DryRun, Sync bool
	DB           *sql.DB
	// Storage is nil when NF_S3_ENDPOINT is empty.
	Storage *storage.Client
	// AllowedDomains is NF_OAUTH_ALLOWED_DOMAINS, already split and trimmed.
	AllowedDomains []string
	// Out receives the report; nil discards it.
	Out io.Writer
}

// Run validates the mapping, reads Asana and the database, and either
// prints the plan (dry run) or writes it. The returned code is the process
// exit code; a non-nil error is the one-line fatal message and always comes
// with a non-zero code.
func Run(ctx context.Context, o Options) (int, error) {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	if o.Token == "" {
		return ExitNoWrites, errors.New("ASANA_TOKEN must be set")
	}
	m, err := ParseMapping(o.Mapping)
	if err != nil {
		var me MappingError
		if !errors.As(err, &me) {
			return ExitNoWrites, err
		}
		writeValidation(out, []MappingError{me})
		return ExitNoWrites, errors.New("mapping has 1 error(s)")
	}
	if errs := m.Validate(o.DryRun); len(errs) > 0 {
		writeValidation(out, errs)
		return ExitNoWrites, fmt.Errorf("mapping has %d error(s)", len(errs))
	}
	if o.DB == nil {
		return ExitNoWrites, errors.New("database is required")
	}

	lk := dbLookup{q: generated.New(o.DB)}
	ws, found, err := lk.workspace(ctx, m.Workspace)
	if err != nil {
		return ExitNoWrites, fmt.Errorf("read workspace: %w", err)
	}
	conn, err := o.DB.Conn(ctx)
	if err != nil {
		return ExitNoWrites, fmt.Errorf("open lock connection: %w", err)
	}
	defer conn.Close()
	if found {
		if err := acquireLock(ctx, conn, ws.ID); err != nil {
			return ExitNoWrites, err
		}
		defer releaseLock(conn, ws.ID)
	}

	client := &asana.Client{BaseURL: o.BaseURL, Token: o.Token}
	workspaces, snap, err := fetchAsana(ctx, client, m)
	if err != nil {
		return ExitNoWrites, err
	}
	plan, err := Prepare(ctx, lk, Input{
		Mapping: m, Sync: o.Sync, Workspaces: workspaces, Snapshot: snap,
		AllowedDomains: o.AllowedDomains, StorageConfigured: o.Storage != nil,
	})
	if err != nil {
		return ExitNoWrites, err
	}
	if len(plan.Errors) > 0 {
		if o.DryRun {
			writeDryRun(out, plan)
		} else {
			writeValidation(out, plan.Errors)
		}
		return ExitNoWrites, fmt.Errorf("mapping has %d error(s) against Asana and the database", len(plan.Errors))
	}

	// Probe skips the projects the guard refuses, so a refusal is decided
	// before any single-item request is made.
	refusals := plan.Refusals()
	if o.Sync {
		plan.Probe(ctx, client)
	}
	if o.DryRun {
		writeDryRun(out, plan)
	}
	if len(refusals) > 0 {
		writeRefusals(out, refusals)
		return ExitNoWrites, fmt.Errorf("sync refused for %d project(s); nothing was written", len(refusals))
	}
	if o.DryRun {
		return ExitOK, nil
	}

	deps := WriteDeps{DB: o.DB, Source: client}
	if o.Storage != nil {
		deps.Storage = o.Storage
	}
	res, err := Write(ctx, plan, deps)
	if res != nil {
		writeRun(out, plan, res)
	}
	switch {
	case res == nil:
		return ExitNoWrites, err
	case err != nil:
		return ExitFailed, err
	case res.Failed() > 0:
		return ExitFailed, nil
	}
	return ExitOK, nil
}

// fetchAsana reads the workspaces the token sees and, when source_workspace
// resolves, the snapshot of the mapped projects. The workspace-level lists
// are read once to resolve the project sources, then again with the
// resolved project gids; an unresolved workspace leaves the snapshot nil
// and Prepare reports it.
func fetchAsana(ctx context.Context, c *asana.Client, m *Mapping) ([]asana.Workspace, *asana.Snapshot, error) {
	workspaces, err := c.ListWorkspaces(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("read Asana workspaces: %w", err)
	}
	sw, merr := ResolveSourceWorkspace(m, workspaces)
	if merr != nil {
		return workspaces, nil, nil
	}
	lists, err := c.Fetch(ctx, asana.Scope{WorkspaceGID: sw.GID})
	if err != nil {
		return nil, nil, fmt.Errorf("read Asana workspace: %w", err)
	}
	sources, _ := ResolveProjectSources(m, lists.Projects)
	var gids []string
	for _, s := range sources {
		if s.GID != "" {
			gids = append(gids, s.GID)
		}
	}
	if len(gids) == 0 {
		return workspaces, lists, nil
	}
	snap, err := c.Fetch(ctx, asana.Scope{WorkspaceGID: sw.GID, ProjectGIDs: gids})
	if err != nil {
		return nil, nil, fmt.Errorf("read Asana projects: %w", err)
	}
	return workspaces, snap, nil
}

// lockName is the per-workspace advisory lock that keeps two runs apart.
func lockName(workspaceID uint32) string {
	return fmt.Sprintf("nodate-flow:importasana:%d", workspaceID)
}

// acquireLock takes the lock without waiting. GET_LOCK belongs to the
// session, so conn must stay the same dedicated connection until release.
func acquireLock(ctx context.Context, conn *sql.Conn, workspaceID uint32) error {
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", lockName(workspaceID)).Scan(&got); err != nil {
		return fmt.Errorf("take lock: %w", err)
	}
	switch {
	case !got.Valid:
		return errors.New("take lock: the database could not grant it")
	case got.Int64 != 1:
		return errors.New("another importasana run holds the lock for this workspace")
	}
	return nil
}

// releaseLock runs on its own context so an interrupted run still lets go.
func releaseLock(conn *sql.Conn, workspaceID uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n sql.NullInt64
	_ = conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", lockName(workspaceID)).Scan(&n)
}
