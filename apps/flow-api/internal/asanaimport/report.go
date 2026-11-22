package asanaimport

import (
	"fmt"
	"io"
	"strings"
)

// noLabel names the section bucket whose sections map to no label.
const noLabel = "(no label)"

// skipReportOrder is the fixed order of the dry-run "skipped:" line.
var skipReportOrder = []string{
	SkipEmptyTitle, SkipParentNotImported, SkipEmptyBody, SkipTooLarge,
	SkipTypeNotAllowed, SkipOutsideMapping, SkipCycle,
}

func (c *Counts) add(o Counts) {
	c.Imported += o.Imported
	c.Updated += o.Updated
	c.Duplicates += o.Duplicates
	c.Deleted += o.Deleted
	c.Kept += o.Kept
	c.Moved += o.Moved
	c.Skipped += o.Skipped
	c.Failed += o.Failed
	c.Rejected += o.Rejected
}

func countsText(c Counts) string {
	return fmt.Sprintf("imported=%d updated=%d duplicates=%d deleted=%d kept=%d moved=%d skipped=%d failed=%d rejected=%d",
		c.Imported, c.Updated, c.Duplicates, c.Deleted, c.Kept, c.Moved, c.Skipped, c.Failed, c.Rejected)
}

// oneLine collapses all whitespace so a message stays on its report line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// nameList renders names and counts as `"<name>"=<n>, ...`, or "none".
func nameList(items []NameCount) string {
	if len(items) == 0 {
		return "none"
	}
	parts := make([]string, len(items))
	for i, it := range items {
		name := it.Name
		if name == "" {
			name = noLabel
		}
		parts[i] = fmt.Sprintf("%q=%d", name, it.Count)
	}
	return strings.Join(parts, ", ")
}

// writeValidation prints the validation errors line and one line per error.
func writeValidation(w io.Writer, errs []MappingError) {
	fmt.Fprintf(w, "validation errors: %d\n", len(errs))
	for _, e := range errs {
		fmt.Fprintf(w, "  - %s\n", oneLine(e.Error()))
	}
}

// writeRefusals prints one refusing line per project the deletion guard stops.
func writeRefusals(w io.Writer, refusals []Refusal) {
	for _, r := range refusals {
		if r.Reason == RefuseEmptySource {
			fmt.Fprintf(w, "refusing: Asana returned no tasks for project %q but %d are synced\n", r.Name, r.Synced)
			continue
		}
		fmt.Fprintf(w, "refusing: project %q would delete %d of %d synced tasks\n", r.Name, r.Lost, r.Synced)
	}
}

func writeProjectBlock(w io.Writer, pp *ProjectPlan) {
	st := &pp.Stats
	fmt.Fprintf(w, "Asana project %s name=%q archived=%t\n", pp.Source.GID, pp.Source.Name, pp.Source.Archived)
	fmt.Fprintf(w, "  mapping: mapped (source: %s)\n", pp.Mapping.Source)
	fmt.Fprintf(w, "  tasks: %d (top-level %d, subtasks %d; default %d, milestone %d, approval %d; completed %d)\n",
		st.Tasks, st.TopLevel, st.Subtasks, st.Default, st.Milestone, st.Approval, st.Completed)
	fmt.Fprintf(w, "  by section: %s\n", nameList(st.BySection))
	fmt.Fprintf(w, "  tags: %s\n", nameList(st.Tags))
	fmt.Fprintf(w, "  comments: %d; attachments: asana=%d external=%d too_large=%d type_not_allowed=%d\n",
		st.Comments, st.AttachmentsAsana, st.AttachmentsExternal, st.Skipped[SkipTooLarge], st.Skipped[SkipTypeNotAllowed])
	fmt.Fprintf(w, "  dependencies: blocks=%d outside_the_mapping=%d cycle=%d\n",
		st.Blocks, st.Skipped[SkipOutsideMapping], st.Skipped[SkipCycle])
	for _, ps := range st.People {
		link := ""
		if ps.Email != "" {
			link = " -> " + ps.Email
		}
		fmt.Fprintf(w, "  person %q (%s) %s%s: assignee=%d creator=%d commenter=%d follower=%d\n",
			ps.Name, ps.GID, ps.Kind, link, ps.Assignee, ps.Creator, ps.Commenter, ps.Follower)
	}
	skips := make([]string, len(skipReportOrder))
	for i, reason := range skipReportOrder {
		skips[i] = fmt.Sprintf("%s=%d", reason, st.Skipped[reason])
	}
	fmt.Fprintf(w, "  skipped: %s\n", strings.Join(skips, ", "))
	fmt.Fprintf(w, "  not imported: custom_fields=%d likes=%d system_stories=%d\n", st.CustomFields, st.Likes, st.SystemStories)
	if pp.ExistingID != 0 {
		fmt.Fprintf(w, "  project %q: reuse existing\n", pp.Name)
	} else {
		fmt.Fprintf(w, "  project %q: create (slug %q)\n", pp.Name, pp.Slug)
	}
}

func writeSyncPlan(w io.Writer, project, entity string, s SyncCounts) {
	fmt.Fprintf(w, "project %q: sync plan %s create=%d update=%d unchanged=%d delete=%d moved=%d kept=%d synced=%d orphaned=%d repeated=%d\n",
		project, entity, s.Create, s.Update, s.Unchanged, s.Delete, s.Moved, s.Kept, s.Synced, s.Orphaned, s.Repeated)
}

// writeDryRun prints the dry-run report: the Asana side of every mapped
// project, the sync plan (only when the plan resolved cleanly and had its
// deletion candidates probed), the sign-in state, the accounts to create and
// the validation errors. Guard refusals are printed after it.
func writeDryRun(w io.Writer, p *Plan) {
	if p.snap != nil && p.SourceWorkspace.GID != "" {
		fmt.Fprintf(w, "Asana workspace %q (%s): projects=%d mapped=%d users=%d tags=%d\n",
			p.SourceWorkspace.Name, p.SourceWorkspace.GID, len(p.snap.Projects), len(p.Projects), len(p.snap.Users), len(p.snap.Tags))
	}
	for _, pp := range p.Projects {
		writeProjectBlock(w, pp)
	}
	if p.Sync && len(p.Errors) == 0 {
		for i, pp := range p.Projects {
			writeSyncPlan(w, pp.Name, "tasks", p.TaskSync(i))
			writeSyncPlan(w, pp.Name, "comments", p.CommentSync(i))
		}
	}
	if p.SignIn.Restricted() {
		fmt.Fprintf(w, "sign-in restriction: domains=%d allowlist=%d\n", p.SignIn.Domains, p.SignIn.Allowlist)
	} else {
		fmt.Fprintln(w, "sign-in restriction: open")
	}
	emails := make([]string, len(p.NewUsers))
	for i, u := range p.NewUsers {
		emails[i] = u.Email
	}
	if len(emails) == 0 {
		fmt.Fprintln(w, "users to create: none")
	} else {
		fmt.Fprintf(w, "users to create: %s\n", strings.Join(emails, ", "))
	}
	writeValidation(w, p.Errors)
}

// writeRun prints what a real run did: per project the entity counters and
// the notes, then the failure reasons, then the total.
func writeRun(w io.Writer, p *Plan, res *WriteResult) {
	var tasks, comments, attachments, deps Counts
	for i, pr := range res.Projects {
		pp := p.Projects[i]
		state := "reused"
		if pr.Created {
			state = "created"
		}
		fmt.Fprintf(w, "project %q (%s): members added=%d existing=%d; tasks %s; comments %s; attachments %s; dependencies %s\n",
			pp.Name, state, pr.MembersAdded, pr.MembersExisting,
			countsText(pr.Tasks), countsText(pr.Comments), countsText(pr.Attachments), countsText(pr.Dependencies))
		n := pp.Notes
		fmt.Fprintf(w, "project %q: notes truncated=%d start_dropped=%d parent_changed=%d attachments_removed_in_asana=%d labels_left=%d completion_gate=%d cancelled_kept=%d slug_ignored=%d identifier_ignored=%d\n",
			pp.Name, n.Truncated, n.StartDropped, n.ParentChanged, n.AttachmentsRemovedInAsana, n.LabelsLeft,
			n.CompletionGate, n.CancelledKept, n.SlugIgnored, n.IdentifierIgnored)
		tasks.add(pr.Tasks)
		comments.add(pr.Comments)
		attachments.add(pr.Attachments)
		deps.add(pr.Dependencies)
	}
	for _, f := range res.Failures {
		fmt.Fprintf(w, "failure: %s\n", oneLine(f))
	}
	fmt.Fprintf(w, "total: tasks %s; comments %s; attachments %s; dependencies %s; projects_created=%d users_created=%d\n",
		countsText(tasks), countsText(comments), countsText(attachments), countsText(deps),
		res.ProjectsCreated, res.UsersCreated)
}
