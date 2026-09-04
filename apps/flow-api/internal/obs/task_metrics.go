package obs

import "github.com/prometheus/client_golang/prometheus"

// taskStateDone is the derived_state value that means a task finished. It
// mirrors the tasks.derived_state enum member of the same name and is the
// sole trigger for the completion counter, matching the schema's definition
// of tasks.completed_at as the time derived_state transitioned to done.
const taskStateDone = "done"

// tasksCreatedTotal counts task rows inserted. It carries no labels: the
// only dimension worth splitting on would be the workspace, and no metric in
// this service exposes a workspace identifier — /metrics is unauthenticated,
// and the cardinality would grow with the tenant count.
var tasksCreatedTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "nf_tasks_created_total",
		Help: "Total number of tasks created.",
	},
)

// tasksCompletedTotal counts transitions whose target state is done. It is
// derived from the same call that records a transition rather than from a
// second instrumentation point, so the two counters cannot disagree about
// what a completion is.
var tasksCompletedTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "nf_tasks_completed_total",
		Help: "Total number of tasks that transitioned to the done state.",
	},
)

// taskStateTransitionsTotal counts derived_state changes, partitioned by the
// state left and the state entered. Both labels range over the
// tasks.derived_state enum (open, waiting, review, done, cancelled), so the
// series count is bounded by the square of a five-member closed set.
var taskStateTransitionsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "nf_task_state_transitions_total",
		Help: "Total number of task derived_state transitions, partitioned by the state left and the state entered.",
	},
	[]string{"from_state", "to_state"},
)

func init() {
	prometheus.MustRegister(tasksCreatedTotal)
	prometheus.MustRegister(tasksCompletedTotal)
	prometheus.MustRegister(taskStateTransitionsTotal)
}

// IncTaskCreated records one newly inserted task row.
//
// Call it from the single task-insert path so every transport — REST, MCP,
// the importer, the intake handlers, and the signal judge's applier — is
// counted by construction rather than by each remembering to.
func IncTaskCreated() {
	tasksCreatedTotal.Inc()
}

// IncTaskTransition records one derived_state change, and additionally a
// completion when to is the done state.
//
// from and to are raw tasks.derived_state values. A call where they are
// equal records nothing: an UPDATE that writes back the value it read moves
// no work through the workflow, and counting it would make the rate track
// write volume instead. Rejecting the pair here rather than at the call site
// keeps the rule in one place, and deriving the completion from the same
// arguments means a task cannot be counted as completed without also being
// counted as having transitioned.
func IncTaskTransition(from, to string) {
	if from == to {
		return
	}
	taskStateTransitionsTotal.WithLabelValues(from, to).Inc()
	if to == taskStateDone {
		tasksCompletedTotal.Inc()
	}
}
