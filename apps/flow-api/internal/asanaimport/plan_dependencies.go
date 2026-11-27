package asanaimport

import (
	"sort"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

type edge struct{ from, to string }

// reaches reports whether to is reachable from from.
func reaches(graph map[string][]string, from, to string) bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == to {
			return true
		}
		for _, m := range graph[n] {
			if !seen[m] {
				seen[m] = true
				stack = append(stack, m)
			}
		}
	}
	return false
}

// planDependencies aligns blocks edges between imported tasks with Asana.
func (p *Plan) planDependencies() {
	rowGID := map[uint32]string{}
	for _, tp := range p.Tasks {
		if tp.Row != nil && tp.Action != ActionSkip {
			rowGID[tp.Row.ID] = tp.GID
		}
	}
	existing := map[edge]bool{}
	var existingOrder []edge
	var existingRows []generated.ListBlockingDependenciesForTasksRow
	for _, e := range p.existing.Blocks {
		f, ok1 := rowGID[e.FromTaskID]
		t, ok2 := rowGID[e.ToTaskID]
		if ok1 && ok2 && !existing[edge{f, t}] {
			existing[edge{f, t}] = true
			existingOrder = append(existingOrder, edge{f, t})
			existingRows = append(existingRows, e)
		}
	}

	desired := map[edge]bool{}
	var creates []*DependencyPlan
	for _, tp := range p.Tasks {
		seen := map[string]bool{}
		for _, dep := range p.snap.Tasks[tp.GID].Task.Dependencies {
			if seen[dep.GID] {
				continue
			}
			seen[dep.GID] = true
			dp := &DependencyPlan{BlockerGID: dep.GID, BlockedGID: tp.GID, Project: tp.Project}
			p.Dependencies = append(p.Dependencies, dp)
			blocker := p.taskByGID[dep.GID]
			if tp.Action == ActionSkip || blocker == nil || blocker.Action == ActionSkip {
				dp.Action, dp.Skip = ActionSkip, SkipOutsideMapping
				continue
			}
			k := edge{dep.GID, tp.GID}
			desired[k] = true
			if existing[k] {
				dp.Action = ActionUnchanged
				continue
			}
			creates = append(creates, dp)
		}
	}

	graph := map[string][]string{}
	for _, k := range existingOrder {
		if desired[k] || !p.Sync {
			graph[k.from] = append(graph[k.from], k.to)
		}
	}
	sorted := append([]*DependencyPlan(nil), creates...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.BlockerGID != b.BlockerGID {
			return gidLess(a.BlockerGID, b.BlockerGID)
		}
		return gidLess(a.BlockedGID, b.BlockedGID)
	})
	for _, dp := range sorted {
		if reaches(graph, dp.BlockedGID, dp.BlockerGID) {
			dp.Action, dp.Skip = ActionSkip, SkipCycle
			continue
		}
		dp.Action = ActionCreate
		graph[dp.BlockerGID] = append(graph[dp.BlockerGID], dp.BlockedGID)
	}

	if p.Sync {
		for i, k := range existingOrder {
			if !desired[k] {
				p.DependencyDeletions = append(p.DependencyDeletions, &DependencyDeletion{Edge: existingRows[i], Project: p.taskByGID[k.to].Project})
			}
		}
	}
}
