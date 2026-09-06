package mcp

// Test-only entry points for the tools whose insert can collide with a row
// that is already there. What each one answers for that collision is
// pinned by a database-backed test in this package.
//
// They go through the registry so the declared floor is bound to the call,
// exactly as it is on a dispatched invocation.
var (
	RunCreateLabel      = toolEntry("create_label")
	RunCreateTimebox    = toolEntry("create_timebox")
	RunAddTaskToTimebox = toolEntry("add_task_to_timebox")
	RunCreatePage       = toolEntry("create_page")
	RunUpdatePage       = toolEntry("update_page")
)
