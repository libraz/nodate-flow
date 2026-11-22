package asanaimporttests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
)

// fakeToken is the personal access token the fake Asana accepts.
// #nosec G101 -- the fake Asana server accepts this value; no real credential
const fakeToken = "fake-asana-pat"

// fakeFile is an attachment and, for an Asana-hosted one, its bytes.
type fakeFile struct {
	asana.Attachment
	data []byte // nil for an external host: no download_url is served
}

// fakeState is everything the fake Asana answers from. Tests edit it
// between runs through fakeAsana.edit.
type fakeState struct {
	workspace asana.Workspace
	users     []asana.User
	tags      []asana.Tag
	projects  []asana.Project
	sections  map[string][]asana.Section // by project gid
	// order is every task gid in creation order; listings follow it.
	order   []string
	tasks   map[string]*asana.Task
	stories map[string][]asana.Story // by task gid
	files   map[string][]fakeFile    // by task gid
	// taskProbe and storyProbe force the status of a single-item GET;
	// without an entry the item answers 200 when it exists, else 404.
	taskProbe  map[string]int
	storyProbe map[string]int
	// downloadStatus forces the status of an attachment's file download.
	downloadStatus map[string]int
}

// addTask appends t to the creation order.
func (s *fakeState) addTask(t asana.Task) {
	if _, ok := s.tasks[t.GID]; !ok {
		s.order = append(s.order, t.GID)
	}
	cp := t
	s.tasks[t.GID] = &cp
}

// removeTask deletes the task from Asana: listings and probes lose it.
func (s *fakeState) removeTask(gid string) {
	delete(s.tasks, gid)
	s.order = slices.DeleteFunc(s.order, func(g string) bool { return g == gid })
}

// removeStory drops a story from its task's listing.
func (s *fakeState) removeStory(taskGID, storyGID string) {
	s.stories[taskGID] = slices.DeleteFunc(s.stories[taskGID], func(st asana.Story) bool { return st.GID == storyGID })
}

// story returns a pointer to a listed story for in-place edits.
func (s *fakeState) story(taskGID, storyGID string) *asana.Story {
	for i := range s.stories[taskGID] {
		if s.stories[taskGID][i].GID == storyGID {
			return &s.stories[taskGID][i]
		}
	}
	return nil
}

// fakeAsana is an httptest server speaking the subset of the Asana REST
// API the importer reads: the client's paths, envelopes and status codes.
type fakeAsana struct {
	srv *httptest.Server

	mu    sync.Mutex
	state *fakeState
	hits  map[string]int
}

func newFakeAsana(t *testing.T) *fakeAsana {
	t.Helper()
	f := &fakeAsana{
		state: &fakeState{
			sections: map[string][]asana.Section{}, tasks: map[string]*asana.Task{},
			stories: map[string][]asana.Story{}, files: map[string][]fakeFile{},
			taskProbe: map[string]int{}, storyProbe: map[string]int{}, downloadStatus: map[string]int{},
		},
		hits: map[string]int{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the API root to hand to the importer.
func (f *fakeAsana) URL() string { return f.srv.URL }

// edit changes the Asana side under the lock.
func (f *fakeAsana) edit(fn func(s *fakeState)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.state)
}

// requests counts the requests whose kind starts with prefix ("" counts all).
func (f *fakeAsana) requests(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for k, v := range f.hits {
		if strings.HasPrefix(k, prefix) {
			n += v
		}
	}
	return n
}

// resetRequests forgets the counted requests.
func (f *fakeAsana) resetRequests() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits = map[string]int{}
}

func writeData(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func writeList(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v, "next_page": nil})
}

func writeStatus(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"message": http.StatusText(status)}}})
}

func (f *fakeAsana) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	// Signed file URLs carry no Authorization header, as in Asana.
	if len(segs) == 2 && segs[0] == "files" {
		f.hits["download:"+segs[1]]++
		if st, ok := s.downloadStatus[segs[1]]; ok {
			writeStatus(w, st)
			return
		}
		for _, list := range s.files {
			for _, ff := range list {
				if ff.GID == segs[1] && ff.data != nil {
					_, _ = w.Write(ff.data)
					return
				}
			}
		}
		writeStatus(w, http.StatusNotFound)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+fakeToken {
		f.hits["unauthorized"]++
		writeStatus(w, http.StatusUnauthorized)
		return
	}

	switch {
	case len(segs) == 1 && segs[0] == "workspaces":
		f.hits["list:workspaces"]++
		writeList(w, []asana.Workspace{s.workspace})
	case len(segs) == 3 && segs[0] == "workspaces" && segs[1] == s.workspace.GID:
		f.hits["list:"+segs[2]]++
		switch segs[2] {
		case "users":
			writeList(w, s.users)
		case "tags":
			writeList(w, s.tags)
		case "projects":
			writeList(w, s.projects)
		default:
			writeStatus(w, http.StatusNotFound)
		}
	case len(segs) == 3 && segs[0] == "projects":
		f.hits["list:project-"+segs[2]]++
		switch segs[2] {
		case "sections":
			writeList(w, s.sections[segs[1]])
		case "tasks":
			writeList(w, f.projectTasks(segs[1]))
		default:
			writeStatus(w, http.StatusNotFound)
		}
	case len(segs) == 3 && segs[0] == "tasks" && segs[2] == "subtasks":
		f.hits["list:subtasks"]++
		writeList(w, f.subtasks(segs[1]))
	case len(segs) == 3 && segs[0] == "tasks" && segs[2] == "stories":
		f.hits["list:stories"]++
		writeList(w, s.stories[segs[1]])
	case len(segs) == 2 && segs[0] == "tasks":
		f.hits["probe-task:"+segs[1]]++
		f.probe(w, s.taskProbe, segs[1], s.tasks[segs[1]] != nil)
	case len(segs) == 2 && segs[0] == "stories":
		f.hits["probe-story:"+segs[1]]++
		f.probe(w, s.storyProbe, segs[1], f.storyExists(segs[1]))
	case len(segs) == 1 && segs[0] == "attachments":
		f.hits["list:attachments"]++
		parent := r.URL.Query().Get("parent")
		out := []asana.Attachment{}
		for _, ff := range s.files[parent] {
			out = append(out, f.attachment(ff))
		}
		writeList(w, out)
	case len(segs) == 2 && segs[0] == "attachments":
		f.hits["attachment:"+segs[1]]++
		for _, list := range s.files {
			for _, ff := range list {
				if ff.GID == segs[1] {
					writeData(w, f.attachment(ff))
					return
				}
			}
		}
		writeStatus(w, http.StatusNotFound)
	default:
		f.hits["unknown:"+r.URL.Path]++
		writeStatus(w, http.StatusNotFound)
	}
}

func (f *fakeAsana) probe(w http.ResponseWriter, forced map[string]int, gid string, exists bool) {
	if st, ok := forced[gid]; ok && st != http.StatusOK {
		writeStatus(w, st)
		return
	}
	if _, ok := forced[gid]; ok || exists {
		writeData(w, map[string]string{"gid": gid})
		return
	}
	writeStatus(w, http.StatusNotFound)
}

func (f *fakeAsana) storyExists(gid string) bool {
	for _, list := range f.state.stories {
		for _, st := range list {
			if st.GID == gid {
				return true
			}
		}
	}
	return false
}

// served is a task as Asana returns it, with num_subtasks counted.
func (f *fakeAsana) served(t *asana.Task) asana.Task {
	cp := *t
	cp.NumSubtasks = len(f.subtasks(t.GID))
	return cp
}

// projectTasks lists the tasks with a membership in the project.
func (f *fakeAsana) projectTasks(projectGID string) []asana.Task {
	out := []asana.Task{}
	for _, g := range f.state.order {
		t := f.state.tasks[g]
		for _, m := range t.Memberships {
			if m.Project.GID == projectGID {
				out = append(out, f.served(t))
				break
			}
		}
	}
	return out
}

func (f *fakeAsana) subtasks(parentGID string) []asana.Task {
	out := []asana.Task{}
	for _, g := range f.state.order {
		t := f.state.tasks[g]
		if t.Parent != nil && t.Parent.GID == parentGID {
			cp := *t
			cp.NumSubtasks = 0
			for _, g2 := range f.state.order {
				if c := f.state.tasks[g2]; c.Parent != nil && c.Parent.GID == t.GID {
					cp.NumSubtasks++
				}
			}
			out = append(out, cp)
		}
	}
	return out
}

func (f *fakeAsana) attachment(ff fakeFile) asana.Attachment {
	a := ff.Attachment
	if ff.data != nil {
		a.DownloadURL = f.srv.URL + "/files/" + a.GID
		a.Size = int64(len(ff.data))
	}
	return a
}
