package acl_test

import (
	"context"
	"database/sql"
	stderrors "errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// -----------------------------------------------------------------------------
// Pure logic tests (no DB)
// -----------------------------------------------------------------------------

func TestWorkspaceRoleAtLeast(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		have acl.WorkspaceRole
		min  acl.WorkspaceRole
		want bool
	}{
		{"owner>=admin", acl.WorkspaceRoleOwner, acl.WorkspaceRoleAdmin, true},
		{"admin>=admin", acl.WorkspaceRoleAdmin, acl.WorkspaceRoleAdmin, true},
		{"member>=admin", acl.WorkspaceRoleMember, acl.WorkspaceRoleAdmin, false},
		{"guest>=member", acl.WorkspaceRoleGuest, acl.WorkspaceRoleMember, false},
		{"owner>=guest", acl.WorkspaceRoleOwner, acl.WorkspaceRoleGuest, true},
		{"unknown<<member", acl.WorkspaceRole("nope"), acl.WorkspaceRoleMember, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.have.AtLeast(tc.min))
		})
	}
}

func TestProjectRoleAtLeast(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		have acl.ProjectRole
		min  acl.ProjectRole
		want bool
	}{
		{"lead>=editor", acl.ProjectRoleLead, acl.ProjectRoleEditor, true},
		{"editor>=lead", acl.ProjectRoleEditor, acl.ProjectRoleLead, false},
		{"commenter>=viewer", acl.ProjectRoleCommenter, acl.ProjectRoleViewer, true},
		{"viewer>=commenter", acl.ProjectRoleViewer, acl.ProjectRoleCommenter, false},
		{"lead>=lead", acl.ProjectRoleLead, acl.ProjectRoleLead, true},
		{"unknown<<viewer", acl.ProjectRole("nope"), acl.ProjectRoleViewer, false},
		{"empty<<viewer", acl.ProjectRoleElevated, acl.ProjectRoleViewer, false},
		{"none<<viewer", acl.ProjectRoleNone, acl.ProjectRoleViewer, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.have.AtLeast(tc.min))
		})
	}
}

func TestWorkspaceRoleIsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		role acl.WorkspaceRole
		want bool
	}{
		{"owner", acl.WorkspaceRoleOwner, true},
		{"admin", acl.WorkspaceRoleAdmin, true},
		{"member", acl.WorkspaceRoleMember, true},
		{"guest", acl.WorkspaceRoleGuest, true},
		{"unknown", acl.WorkspaceRole("nope"), false},
		{"empty", acl.WorkspaceRole(""), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.role.IsValid())
		})
	}
}

func TestProjectRoleIsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		role acl.ProjectRole
		want bool
	}{
		{"lead", acl.ProjectRoleLead, true},
		{"editor", acl.ProjectRoleEditor, true},
		{"commenter", acl.ProjectRoleCommenter, true},
		{"viewer", acl.ProjectRoleViewer, true},
		{"elevated/empty", acl.ProjectRoleElevated, true},
		{"none/internal", acl.ProjectRoleNone, true},
		{"unknown", acl.ProjectRole("nope"), false},
		{"capitalized", acl.ProjectRole("Lead"), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.role.IsValid())
		})
	}
}

func TestTaskVisibilityFilter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		userID    uint32
		role      acl.WorkspaceRole
		wantEmpty bool
		wantArgs  int
	}{
		{"admin sees all", 1, acl.WorkspaceRoleAdmin, true, 0},
		{"owner sees all", 1, acl.WorkspaceRoleOwner, true, 0},
		{"member filtered", 42, acl.WorkspaceRoleMember, false, 3},
		{"guest filtered", 99, acl.WorkspaceRoleGuest, false, 3},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			frag, args := acl.TaskVisibilityFilter(tc.userID, tc.role)
			if tc.wantEmpty {
				require.Empty(t, frag)
				require.Empty(t, args)
				return
			}
			require.NotEmpty(t, frag)
			require.Len(t, args, tc.wantArgs)
			for _, a := range args {
				require.Equal(t, tc.userID, a.(uint32))
			}
		})
	}
}

// -----------------------------------------------------------------------------
// DB-backed tests
//
// These exercise the full Check* / Resolve* functions against the canonical
// schema seeded in a shared MySQL container. They cover every (layer ×
// allowed/denied/not-found) combination once at the package level so the
// HTTP middleware and MCP wrappers can keep their tests minimal.
// -----------------------------------------------------------------------------

// aclFixture holds the seeded ids needed for cross-cutting ACL assertions.
type aclFixture struct {
	wsID      uint32
	wsPub     uuid.UUID
	prjID     uint32
	prjPub    uuid.UUID
	otherWsID uint32

	taskPublicID  uint32
	taskPublicPub uuid.UUID
	taskProject   uint32
	taskPrivate   uint32

	ownerUserID    uint32
	memberUserID   uint32
	outsiderUserID uint32
	creatorUserID  uint32

	adminUserID uint32
}

// seedFixture inserts a self-contained ACL fixture into the database.
// It uses raw SQL because the test is exercising the raw SQL layer; the
// production sqlc / API paths perform the same inserts.
func seedFixture(t *testing.T, db *sql.DB) *aclFixture {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	insertUser := func(suffix string) uint32 {
		pub := uuid.Must(uuid.NewV7())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO users (public_id, email, display_name, locale)
			 VALUES (?, ?, ?, 'en')`,
			pub[:], "acl-"+suffix+"@example.test", "ACL "+suffix)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	insertWorkspace := func(slug string) (uint32, uuid.UUID) {
		pub := uuid.Must(uuid.NewV7())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
			pub[:], slug, "Workspace "+slug)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id), pub //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	insertWorkspaceMember := func(wsID, userID uint32, role string) {
		pub := uuid.Must(uuid.NewV7())
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_members (public_id, workspace_id, user_id, role)
			 VALUES (?, ?, ?, ?)`,
			pub[:], wsID, userID, role)
		require.NoError(t, err)
	}

	insertProject := func(wsID uint32, slug string) (uint32, uuid.UUID) {
		pub := uuid.Must(uuid.NewV7())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO projects (public_id, workspace_id, slug, name)
			 VALUES (?, ?, ?, ?)`,
			pub[:], wsID, slug, "Project "+slug)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id), pub //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	insertProjectMember := func(wsID, prjID, userID uint32, role string) {
		pub := uuid.Must(uuid.NewV7())
		_, err := tx.ExecContext(ctx,
			`INSERT INTO project_members (public_id, workspace_id, project_id, user_id, role)
			 VALUES (?, ?, ?, ?, ?)`,
			pub[:], wsID, prjID, userID, role)
		require.NoError(t, err)
	}

	insertTask := func(wsID, prjID uint32, taskNum int, visibility string, creator sql.NullInt32) (uint32, uuid.UUID) {
		pub := uuid.Must(uuid.NewV7())
		res, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			pub[:], wsID, prjID, taskNum, "Task "+visibility, visibility, creator)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id), pub //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	insertInstanceAdmin := func(userID uint32) {
		pub := uuid.Must(uuid.NewV7())
		_, err := tx.ExecContext(ctx,
			`INSERT INTO instance_admins (public_id, user_id, enabled) VALUES (?, ?, TRUE)`,
			pub[:], userID)
		require.NoError(t, err)
	}

	suffix := uuid.New().String()[:8]
	owner := insertUser("owner-" + suffix)
	member := insertUser("member-" + suffix)
	outsider := insertUser("outsider-" + suffix)
	creator := insertUser("creator-" + suffix)
	adminUser := insertUser("admin-" + suffix)

	wsID, wsPub := insertWorkspace("ws-acl-" + suffix)
	insertWorkspaceMember(wsID, owner, "owner")
	insertWorkspaceMember(wsID, member, "member")
	insertWorkspaceMember(wsID, creator, "member")

	otherWsID, _ := insertWorkspace("ws-other-" + suffix)
	insertWorkspaceMember(otherWsID, outsider, "owner")

	prjID, prjPub := insertProject(wsID, "prj-acl-"+suffix)
	insertProjectMember(wsID, prjID, member, "editor")
	// Note: 'creator' is a workspace member but NOT a project member,
	// so they only get to a private task they created via the visibility path.

	taskPublic, taskPublicPub := insertTask(wsID, prjID, 1, "public", sql.NullInt32{Int32: int32(creator), Valid: true}) //#nosec G115 -- test creator user id, fits int32
	taskProject, _ := insertTask(wsID, prjID, 2, "project", sql.NullInt32{Int32: int32(creator), Valid: true})           //#nosec G115 -- test creator user id, fits int32
	taskPrivate, _ := insertTask(wsID, prjID, 3, "private", sql.NullInt32{Int32: int32(creator), Valid: true})           //#nosec G115 -- test creator user id, fits int32

	insertInstanceAdmin(adminUser)

	require.NoError(t, tx.Commit())
	committed = true

	return &aclFixture{
		wsID:           wsID,
		wsPub:          wsPub,
		prjID:          prjID,
		prjPub:         prjPub,
		otherWsID:      otherWsID,
		taskPublicID:   taskPublic,
		taskPublicPub:  taskPublicPub,
		taskProject:    taskProject,
		taskPrivate:    taskPrivate,
		ownerUserID:    owner,
		memberUserID:   member,
		outsiderUserID: outsider,
		creatorUserID:  creator,
		adminUserID:    adminUser,
	}
}

// requireSpec asserts err is an APIError with the expected spec.
func requireSpec(t *testing.T, err error, spec *apierrors.Spec) {
	t.Helper()
	require.Error(t, err)
	var ae *apierrors.APIError
	require.True(t, stderrors.As(err, &ae), "want *apierrors.APIError, got %T: %v", err, err)
	require.NotNil(t, ae.Spec)
	require.Equalf(t, spec.Code, ae.Spec.Code, "wrong error code: got %s want %s", ae.Spec.Code, spec.Code)
}

// requireIntegration gates the DB-backed tests in this package. Once
// integration mode is on the test boots the shared MySQL testcontainer;
// if Docker is unreachable, helpers.StartShared fails the test rather
// than skipping it.
func requireIntegration(t *testing.T) {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
}

// TestACLLayered exercises every (layer × outcome) combination.
func TestACLLayered(t *testing.T) {
	requireIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedFixture(t, db)
	ctx := context.Background()

	// -----------------------------------------------------------------
	// Layer 1: instance admin
	// -----------------------------------------------------------------
	t.Run("instance/admin/allowed", func(t *testing.T) {
		require.NoError(t, acl.CheckInstanceAdmin(ctx, db, fx.adminUserID))
	})
	t.Run("instance/admin/denied", func(t *testing.T) {
		err := acl.CheckInstanceAdmin(ctx, db, fx.memberUserID)
		requireSpec(t, err, apierrors.AuthPermissionInstanceAdminRequired)
	})
	t.Run("instance/admin/not_found", func(t *testing.T) {
		err := acl.CheckInstanceAdmin(ctx, db, 999_999_999)
		requireSpec(t, err, apierrors.AuthPermissionInstanceAdminRequired)
	})

	// -----------------------------------------------------------------
	// Layer 2: workspace
	// -----------------------------------------------------------------
	t.Run("workspace/resolve/allowed", func(t *testing.T) {
		access, err := acl.ResolveWorkspaceAccess(ctx, db, fx.wsPub, fx.memberUserID)
		require.NoError(t, err)
		require.Equal(t, fx.wsID, access.ID)
		require.Equal(t, acl.WorkspaceRoleMember, access.Role)
	})
	t.Run("workspace/resolve/owner", func(t *testing.T) {
		access, err := acl.ResolveWorkspaceAccess(ctx, db, fx.wsPub, fx.ownerUserID)
		require.NoError(t, err)
		require.Equal(t, acl.WorkspaceRoleOwner, access.Role)
	})
	t.Run("workspace/resolve/denied", func(t *testing.T) {
		_, err := acl.ResolveWorkspaceAccess(ctx, db, fx.wsPub, fx.outsiderUserID)
		requireSpec(t, err, apierrors.WsWorkspaceAccessDenied)
	})
	t.Run("workspace/resolve/not_found", func(t *testing.T) {
		_, err := acl.ResolveWorkspaceAccess(ctx, db, uuid.Must(uuid.NewV7()), fx.memberUserID)
		requireSpec(t, err, apierrors.WsWorkspaceNotFound)
	})

	// -----------------------------------------------------------------
	// Layer 3: project
	// -----------------------------------------------------------------
	t.Run("project/resolve_in_ws/allowed", func(t *testing.T) {
		id, err := acl.ResolveProjectInWorkspace(ctx, db, fx.wsID, fx.prjPub)
		require.NoError(t, err)
		require.Equal(t, fx.prjID, id)
	})
	t.Run("project/resolve_in_ws/wrong_ws", func(t *testing.T) {
		_, err := acl.ResolveProjectInWorkspace(ctx, db, fx.otherWsID, fx.prjPub)
		requireSpec(t, err, apierrors.WsProjectNotFound)
	})
	t.Run("project/resolve_in_ws/not_found", func(t *testing.T) {
		_, err := acl.ResolveProjectInWorkspace(ctx, db, fx.wsID, uuid.Must(uuid.NewV7()))
		requireSpec(t, err, apierrors.WsProjectNotFound)
	})

	t.Run("project/membership/direct_member", func(t *testing.T) {
		role, isMember, err := acl.CheckProjectMembership(
			ctx, db, fx.wsID, fx.prjID, fx.memberUserID, acl.WorkspaceRoleMember, nil)
		require.NoError(t, err)
		require.True(t, isMember)
		require.Equal(t, acl.ProjectRoleEditor, role)
	})
	t.Run("project/membership/elevated_via_owner", func(t *testing.T) {
		// Owner is not a project_members row, but workspace owner elevates.
		role, isMember, err := acl.CheckProjectMembership(
			ctx, db, fx.wsID, fx.prjID, fx.ownerUserID, acl.WorkspaceRoleOwner, nil)
		require.NoError(t, err)
		require.False(t, isMember)
		require.Equal(t, acl.ProjectRoleElevated, role)
	})
	t.Run("project/membership/denied", func(t *testing.T) {
		// 'creator' is workspace member but not a project member, no
		// elevated role -> denied.
		_, _, err := acl.CheckProjectMembership(
			ctx, db, fx.wsID, fx.prjID, fx.creatorUserID, acl.WorkspaceRoleMember, nil)
		requireSpec(t, err, apierrors.WsProjectAccessDenied)
	})
	t.Run("project/membership/denied_custom_spec", func(t *testing.T) {
		_, _, err := acl.CheckProjectMembership(
			ctx, db, fx.wsID, fx.prjID, fx.creatorUserID, acl.WorkspaceRoleMember,
			apierrors.WsTaskAccessDenied)
		requireSpec(t, err, apierrors.WsTaskAccessDenied)
	})
	t.Run("project/membership/lookup_non_member_no_error", func(t *testing.T) {
		role, isMember, err := acl.LookupProjectMembership(
			ctx, db, fx.wsID, fx.prjID, fx.creatorUserID, acl.WorkspaceRoleMember)
		require.NoError(t, err)
		require.False(t, isMember)
		require.Equal(t, acl.ProjectRoleNone, role)
	})

	// -----------------------------------------------------------------
	// Layer 4: task
	// -----------------------------------------------------------------
	t.Run("task/resolve_by_pub/allowed", func(t *testing.T) {
		rec, err := acl.ResolveTaskByPublicID(ctx, db, fx.taskPublicPub)
		require.NoError(t, err)
		require.Equal(t, fx.taskPublicID, rec.ID)
		require.Equal(t, fx.wsID, rec.WorkspaceID)
		require.Equal(t, fx.prjID, rec.ProjectID)
		require.Equal(t, acl.TaskVisibilityPublic, rec.Visibility)
	})
	t.Run("task/resolve_by_pub/not_found", func(t *testing.T) {
		_, err := acl.ResolveTaskByPublicID(ctx, db, uuid.Must(uuid.NewV7()))
		requireSpec(t, err, apierrors.WsTaskNotFound)
	})
	t.Run("task/resolve_in_ws/allowed", func(t *testing.T) {
		id, err := acl.ResolveTaskInWorkspace(ctx, db, fx.wsID, fx.taskPublicPub)
		require.NoError(t, err)
		require.Equal(t, fx.taskPublicID, id)
	})
	t.Run("task/resolve_in_ws/wrong_ws", func(t *testing.T) {
		_, err := acl.ResolveTaskInWorkspace(ctx, db, fx.otherWsID, fx.taskPublicPub)
		requireSpec(t, err, apierrors.WsTaskNotFound)
	})

	// Visibility checks.
	type visCase struct {
		name            string
		taskID          uint32
		visibility      acl.TaskVisibility
		creator         sql.NullInt32
		userID          uint32
		wsRole          acl.WorkspaceRole
		isProjectMember bool
		wantSpec        *apierrors.Spec // nil => allowed
	}
	creatorVal := sql.NullInt32{Int32: int32(fx.creatorUserID), Valid: true} //#nosec G115 -- test creator user id, fits int32
	visCases := []visCase{
		// public: any workspace member already verified upstream
		{"public/member", fx.taskPublicID, acl.TaskVisibilityPublic, creatorVal, fx.memberUserID, acl.WorkspaceRoleMember, true, nil},
		{"public/non_member_ok", fx.taskPublicID, acl.TaskVisibilityPublic, creatorVal, fx.creatorUserID, acl.WorkspaceRoleMember, false, nil},

		// project: requires direct project membership or elevation
		{"project/direct_member", fx.taskProject, acl.TaskVisibilityProject, creatorVal, fx.memberUserID, acl.WorkspaceRoleMember, true, nil},
		{"project/elevated_admin", fx.taskProject, acl.TaskVisibilityProject, creatorVal, fx.memberUserID, acl.WorkspaceRoleAdmin, false, nil},
		{"project/elevated_owner", fx.taskProject, acl.TaskVisibilityProject, creatorVal, fx.ownerUserID, acl.WorkspaceRoleOwner, false, nil},
		{"project/non_member_denied", fx.taskProject, acl.TaskVisibilityProject, creatorVal, fx.creatorUserID, acl.WorkspaceRoleMember, false, apierrors.WsTaskNotFound},

		// private: requires being task actor / creator, unless elevated
		{"private/elevated_admin", fx.taskPrivate, acl.TaskVisibilityPrivate, creatorVal, fx.memberUserID, acl.WorkspaceRoleAdmin, true, nil},
		{"private/creator", fx.taskPrivate, acl.TaskVisibilityPrivate, creatorVal, fx.creatorUserID, acl.WorkspaceRoleMember, false, nil},
		{"private/non_actor_denied", fx.taskPrivate, acl.TaskVisibilityPrivate, creatorVal, fx.memberUserID, acl.WorkspaceRoleMember, true, apierrors.WsTaskNotFound},
	}
	for _, c := range visCases {
		c := c
		t.Run("task/visibility/"+c.name, func(t *testing.T) {
			rec := acl.TaskRecord{
				ID:              c.taskID,
				WorkspaceID:     fx.wsID,
				ProjectID:       fx.prjID,
				Visibility:      c.visibility,
				CreatedByUserID: c.creator,
			}
			err := acl.CheckTaskVisibility(ctx, db, rec, c.userID, c.wsRole, c.isProjectMember)
			if c.wantSpec == nil {
				require.NoError(t, err)
				return
			}
			requireSpec(t, err, c.wantSpec)
		})
	}

	// IsTaskActor: confirm true once we add an actor row.
	t.Run("task/actor/positive_after_insert", func(t *testing.T) {
		// Add member as task_actor on the private task.
		actorPub := uuid.Must(uuid.NewV7())
		_, err := db.ExecContext(ctx,
			`INSERT INTO task_actors (public_id, workspace_id, task_id, user_id, kind, role)
			 VALUES (?, ?, ?, ?, 'user', 'assignee')`,
			actorPub[:], fx.wsID, fx.taskPrivate, fx.memberUserID)
		require.NoError(t, err)
		ok, err := acl.IsTaskActor(ctx, db, fx.taskPrivate, fx.memberUserID)
		require.NoError(t, err)
		require.True(t, ok)
		// Now visibility should allow the member as well.
		rec := acl.TaskRecord{
			ID:              fx.taskPrivate,
			WorkspaceID:     fx.wsID,
			ProjectID:       fx.prjID,
			Visibility:      acl.TaskVisibilityPrivate,
			CreatedByUserID: creatorVal,
		}
		require.NoError(t, acl.CheckTaskVisibility(ctx, db, rec, fx.memberUserID, acl.WorkspaceRoleMember, true))
	})

	t.Run("task/authorize/shared_matrix", func(t *testing.T) {
		access, err := acl.AuthorizeTaskAccess(ctx, db, fx.taskPublicPub, fx.creatorUserID)
		require.NoError(t, err)
		require.Equal(t, fx.taskPublicID, access.Task.ID)
		require.False(t, access.IsProjectMember)

		_, err = acl.AuthorizeTaskAccess(ctx, db, uuidFromTaskID(t, db, fx.taskProject), fx.creatorUserID)
		requireSpec(t, err, apierrors.WsTaskNotFound)

		access, err = acl.AuthorizeTaskAccess(ctx, db, uuidFromTaskID(t, db, fx.taskProject), fx.memberUserID)
		require.NoError(t, err)
		require.True(t, access.IsProjectMember)

		nonActorUserID := insertWorkspaceOnlyUser(t, db, fx.wsID)
		_, err = acl.AuthorizeTaskAccess(ctx, db, uuidFromTaskID(t, db, fx.taskPrivate), nonActorUserID)
		requireSpec(t, err, apierrors.WsTaskNotFound)
	})
}

func insertWorkspaceOnlyUser(t *testing.T, db *sql.DB, wsID uint32) uint32 {
	t.Helper()
	pub := uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO users (public_id, email, display_name, locale)
		 VALUES (?, ?, ?, 'en')`,
		pub[:], "acl-non-actor-"+uuid.New().String()+"@example.test", "ACL Non Actor")
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)

	memberPub := uuid.Must(uuid.NewV7())
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role)
		 VALUES (?, ?, ?, 'member')`,
		memberPub[:], wsID, uint32(id)) //#nosec G115 -- LastInsertId in test seed, fits uint32
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
}

func uuidFromTaskID(t *testing.T, db *sql.DB, taskID uint32) uuid.UUID {
	t.Helper()
	var raw []byte
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT public_id FROM tasks WHERE id = ?`, taskID).Scan(&raw))
	got, err := uuid.FromBytes(raw)
	require.NoError(t, err)
	return got
}
