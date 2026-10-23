package mcp

// Tool entry points for the regression tests that hold MCP tools to the
// semantics of their REST counterparts.
var (
	RunListProjects        = toolEntry("list_projects")
	RunListPages           = toolEntry("list_pages")
	RunSearchTasks         = toolEntry("search_tasks")
	RunApplySteps          = toolEntry("apply_steps")
	RunSmartCreateTask     = toolEntry("smart_create_task")
	RunListIntakeItems     = toolEntry("list_intake_items")
	RunConvertIntakeToTask = toolEntry("convert_intake_to_task")
	RunProposeLens         = toolEntry("propose_lens")
)
