package asana

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"sync"
)

// Scope selects what Fetch reads.
type Scope struct {
	WorkspaceGID string
	// ProjectGIDs are the mapped projects whose tasks are fetched, in mapping order.
	ProjectGIDs []string
}

// Repeat records a gid returned more than once by a single listing.
type Repeat struct {
	Parent string // parent task gid for a subtask listing, "" for the project listing
	GID    string
}

// ProjectSnapshot is one mapped project's content.
type ProjectSnapshot struct {
	Project  Project
	Sections []Section
	// Order lists every task gid once: the project listing order, each task
	// followed by its descendants depth-first.
	Order []string
	// Direct holds the gids that appeared in the project's own task listing;
	// the rest of Order are descendants reached through subtasks.
	Direct   map[string]bool
	Repeated []Repeat
}

// TaskDetail is a task with its comments and attachments.
type TaskDetail struct {
	Task        Task
	Comments    []Story // resource_subtype == comment_added only
	Attachments []Attachment
	// SystemStories counts the stories dropped as non-comments; Likes sums
	// num_likes over all stories of the task.
	SystemStories int
	Likes         int
}

// Snapshot is the result of Fetch.
type Snapshot struct {
	WorkspaceGID string
	Users        []User
	Tags         []Tag
	Projects     []Project // every workspace project, archived included
	Mapped       map[string]*ProjectSnapshot
	Tasks        map[string]*TaskDetail
}

const detailWorkers = maxInFlight

// Fetch reads the workspace-level lists and, for each mapped project, its
// sections, tasks, recursive subtasks, comments and attachments. Any error
// aborts the fetch; no partial snapshot is returned.
func (c *Client) Fetch(ctx context.Context, scope Scope) (*Snapshot, error) {
	if scope.WorkspaceGID == "" {
		return nil, fmt.Errorf("asana: workspace gid is required")
	}
	ws := "/workspaces/" + url.PathEscape(scope.WorkspaceGID)
	snap := &Snapshot{
		WorkspaceGID: scope.WorkspaceGID,
		Mapped:       map[string]*ProjectSnapshot{},
		Tasks:        map[string]*TaskDetail{},
	}
	var err error
	if snap.Users, err = list[User](ctx, c, ws+"/users", url.Values{"opt_fields": {userFields}}); err != nil {
		return nil, err
	}
	if snap.Tags, err = list[Tag](ctx, c, ws+"/tags", url.Values{"opt_fields": {nameFields}}); err != nil {
		return nil, err
	}
	if snap.Projects, err = list[Project](ctx, c, ws+"/projects", url.Values{"opt_fields": {projectFields}}); err != nil {
		return nil, err
	}
	known := map[string]Project{}
	for _, p := range snap.Projects {
		known[p.GID] = p
	}

	f := &fetcher{c: c, subs: map[string]*subtaskListing{}, tasks: map[string]Task{}}
	for _, pgid := range scope.ProjectGIDs {
		if snap.Mapped[pgid] != nil {
			continue
		}
		ps := &ProjectSnapshot{Project: known[pgid], Direct: map[string]bool{}}
		if ps.Project.GID == "" {
			ps.Project.GID = pgid
		}
		pp := "/projects/" + url.PathEscape(pgid)
		if ps.Sections, err = list[Section](ctx, c, pp+"/sections", url.Values{"opt_fields": {nameFields}}); err != nil {
			return nil, err
		}
		tasks, err := list[Task](ctx, c, pp+"/tasks", url.Values{
			"opt_fields":      {taskFields},
			"completed_since": {"1970-01-01T00:00:00Z"},
		})
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		listed := map[string]bool{}
		for _, t := range tasks {
			if listed[t.GID] {
				ps.Repeated = append(ps.Repeated, Repeat{GID: t.GID})
				continue
			}
			listed[t.GID] = true
			ps.Direct[t.GID] = true
			if err := f.visit(ctx, ps, seen, t); err != nil {
				return nil, err
			}
		}
		// Sorted so the report is independent of traversal order.
		sort.Slice(ps.Repeated, func(i, j int) bool {
			if ps.Repeated[i].Parent != ps.Repeated[j].Parent {
				return ps.Repeated[i].Parent < ps.Repeated[j].Parent
			}
			return ps.Repeated[i].GID < ps.Repeated[j].GID
		})
		snap.Mapped[pgid] = ps
	}

	if err := f.fetchDetails(ctx, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

type subtaskListing struct {
	tasks    []Task
	repeated []string
}

type fetcher struct {
	c     *Client
	subs  map[string]*subtaskListing
	tasks map[string]Task
}

// visit appends t to ps.Order and walks its subtasks depth-first.
func (f *fetcher) visit(ctx context.Context, ps *ProjectSnapshot, seen map[string]bool, t Task) error {
	if seen[t.GID] {
		return nil
	}
	seen[t.GID] = true
	ps.Order = append(ps.Order, t.GID)
	if _, ok := f.tasks[t.GID]; !ok {
		f.tasks[t.GID] = t
	}
	if t.NumSubtasks == 0 {
		return nil
	}
	sub, err := f.subtasks(ctx, t.GID)
	if err != nil {
		return err
	}
	for _, g := range sub.repeated {
		ps.Repeated = append(ps.Repeated, Repeat{Parent: t.GID, GID: g})
	}
	for _, st := range sub.tasks {
		if err := f.visit(ctx, ps, seen, st); err != nil {
			return err
		}
	}
	return nil
}

func (f *fetcher) subtasks(ctx context.Context, gid string) (*subtaskListing, error) {
	if s, ok := f.subs[gid]; ok {
		return s, nil
	}
	raw, err := list[Task](ctx, f.c, "/tasks/"+url.PathEscape(gid)+"/subtasks", url.Values{"opt_fields": {taskFields}})
	if err != nil {
		return nil, err
	}
	s := &subtaskListing{}
	seen := map[string]bool{}
	for _, t := range raw {
		if seen[t.GID] {
			s.repeated = append(s.repeated, t.GID)
			continue
		}
		seen[t.GID] = true
		s.tasks = append(s.tasks, t)
	}
	f.subs[gid] = s
	return s, nil
}

// fetchDetails reads comments and attachments of every collected task.
func (f *fetcher) fetchDetails(ctx context.Context, snap *Snapshot) error {
	gids := make([]string, 0, len(f.tasks))
	for g := range f.tasks {
		gids = append(gids, g)
	}
	sort.Strings(gids)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	ch := make(chan string)
	for i := 0; i < detailWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range ch {
				d, err := f.detail(ctx, f.tasks[g])
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
						cancel()
					}
				} else {
					snap.Tasks[g] = d
				}
				mu.Unlock()
			}
		}()
	}
	for _, g := range gids {
		ch <- g
	}
	close(ch)
	wg.Wait()
	return firstErr
}

func (f *fetcher) detail(ctx context.Context, t Task) (*TaskDetail, error) {
	d := &TaskDetail{Task: t}
	gid := url.PathEscape(t.GID)
	stories, err := list[Story](ctx, f.c, "/tasks/"+gid+"/stories", url.Values{"opt_fields": {storyFields}})
	if err != nil {
		return nil, err
	}
	for _, s := range stories {
		d.Likes += s.NumLikes
		if s.ResourceSubtype == StoryCommentAdded {
			d.Comments = append(d.Comments, s)
		} else {
			d.SystemStories++
		}
	}
	d.Attachments, err = list[Attachment](ctx, f.c, "/attachments", url.Values{
		"parent":     {t.GID},
		"opt_fields": {attachmentFields},
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}
