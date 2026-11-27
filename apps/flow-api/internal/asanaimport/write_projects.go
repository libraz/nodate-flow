package asanaimport

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/projectkit"
	"github.com/libraz/nodate-flow/packages/go-shared/memberkit"
)

// users creates the mapping's new accounts, one transaction each.
func (w *writer) users(ctx context.Context) error {
	ws := w.p.Workspace
	for _, u := range w.p.NewUsers {
		if err := ctx.Err(); err != nil {
			return err
		}
		var id uint32
		pub := types.New()
		err := w.inTx(ctx, "CreateUser", func(ctx context.Context, tx *dbretry.Tx) error {
			q := generated.New(tx)
			raw, err := q.CreateStubUser(ctx, generated.CreateStubUserParams{
				PublicID: pub, Email: u.Email, DisplayName: u.Name, Locale: "en",
				Timezone: ws.Timezone, Country: ws.Country, ThemePreference: generated.UsersThemePreferenceSystem,
			})
			if err != nil {
				return err
			}
			id = uint32(raw) //#nosec G115 -- LastInsertId of users.id, fits uint32 within realistic deployments
			if _, err := memberkit.AddWorkspaceMember(ctx, tx, memberkit.AddWorkspaceMemberArgs{
				WorkspaceID: ws.ID, UserID: id, Role: memberkit.RoleMember,
				InvitedByUserID: w.p.Importer.ID, EnsurePersonalCalendar: true,
			}); err != nil {
				return err
			}
			// An open deployment gets no row: one would restrict sign-in for everyone.
			if !w.p.SignIn.Restricted() {
				return nil
			}
			return q.UpsertOauthSigninAllowlistEntry(ctx, generated.UpsertOauthSigninAllowlistEntryParams{
				PublicID: types.New(), AddedByUserID: nullInt32(w.p.Importer.ID),
				EntryKind: generated.OauthSigninAllowlistEntryKindEmail, EntryValue: u.Email,
				Notes: sql.NullString{String: stubNotes, Valid: true},
			})
		})
		if err != nil {
			w.fail(&w.res.UsersFailed, fmt.Sprintf("user %q", u.Email), err)
			continue
		}
		w.res.UsersCreated++
		w.accounts[u.Email] = Account{ID: id, PublicID: pub, Email: u.Email, Name: u.Name, Enabled: true}
	}
	return nil
}

// projects creates or reuses each mapped project and adds its members.
func (w *writer) projects(ctx context.Context) error {
	ws := w.p.Workspace
	for i, pp := range w.p.Projects {
		if err := ctx.Err(); err != nil {
			return err
		}
		pr := &w.res.Projects[i]
		w.projectIDs[i] = pp.ExistingID
		if pp.ExistingID == 0 {
			var created projectkit.Project
			err := w.inTx(ctx, "CreateProject", func(ctx context.Context, tx *dbretry.Tx) error {
				var err error
				created, err = projectkit.Create(ctx, tx, projectkit.CreateArgs{
					WorkspaceID: ws.ID, Slug: pp.Slug,
					Identifier: sql.NullString{String: pp.Identifier, Valid: pp.Identifier != ""},
					Name:       pp.Name, LeadUserID: w.p.Importer.ID,
				})
				return err
			})
			if err != nil {
				w.fail(&w.res.ProjectsFailed, fmt.Sprintf("project %q", pp.Name), err)
				continue
			}
			audit.New(w.q).Record(ctx, audit.Entry{
				Action: "project.create", ActorID: w.p.Importer.ID, WorkspaceID: ws.ID,
				ResourceType: "project", ResourceID: created.PublicID.String(),
				Metadata: map[string]any{"slug": pp.Slug, "name": pp.Name},
			})
			w.projectIDs[i] = created.ID
			pr.Created = true
			w.res.ProjectsCreated++
		}
		for _, m := range pp.Members {
			if m.Existing {
				pr.MembersExisting++
				continue
			}
			what := fmt.Sprintf("project %q member %q", pp.Name, m.Email)
			uid, err := w.userID(m.Email)
			if err != nil {
				w.fail(&pr.MembersFailed, what, err)
				continue
			}
			var added bool
			err = w.inTx(ctx, "AddProjectMember", func(ctx context.Context, tx *dbretry.Tx) error {
				var err error
				added, err = projectkit.AddMember(ctx, tx, projectkit.MemberArgs{
					WorkspaceID: ws.ID, ProjectID: w.projectIDs[i], UserID: uid,
					Role: m.Role, PublicID: types.New(), AddedAt: time.Now(),
				})
				return err
			})
			switch {
			case err != nil:
				w.fail(&pr.MembersFailed, what, err)
			case added:
				pr.MembersAdded++
			default:
				pr.MembersExisting++
			}
		}
	}
	return nil
}
