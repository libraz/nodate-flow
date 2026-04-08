package constraint

// Templates in this file demonstrate that legacy "Cycle" (sprint) and
// "Goal" concepts from older project tools can be expressed entirely
// via the Phase 3 constraint primitives, with no new DB tables.
//
// - 3.MIG-1: Sprint — a sprint is just a collection of tasks that must
//   all be completed (dependency.all_done) before a target cutoff
//   (time.due_before). No `sprints` or `cycles` table is required.
//
// - 3.MIG-2: Goal — a goal is just the dependency.all_done of its
//   child tasks. A task belongs to a goal iff it appears in the goal
//   task's dependency list. No `goals` table is required.

// Sprint returns a Constraint that is satisfied when every task in
// memberTaskPublicIDs is done AND the due_on of the current task is
// strictly before the given sprint end date (YYYY-MM-DD UTC).
func Sprint(endDate string, memberTaskPublicIDs []string) Constraint {
	return Constraint{
		Op: OpAnd,
		Terms: []Constraint{
			{Op: OpTimeDueBefore, Arg: endDate},
			{Op: OpDepAllDone, TaskIDs: memberTaskPublicIDs},
		},
	}
}

// Goal returns a Constraint that is satisfied when every child task is
// done. A Phase 3 "goal" is nothing more than a parent task whose
// constraint is this, plus dependency edges to its children.
func Goal(childTaskPublicIDs []string) Constraint {
	return Constraint{
		Op:      OpDepAllDone,
		TaskIDs: childTaskPublicIDs,
	}
}
