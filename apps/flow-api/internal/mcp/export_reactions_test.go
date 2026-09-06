package mcp

// The reaction tools are driven from an external test package, which needs
// the same registry-bound entry points the other tools are reached through.
// Registration is where the declared floor is bound to the call, so an entry
// point that named the implementation directly would run the tool with no
// floor at all and prove nothing about what a dispatched call does.

var (
	RunAddReaction   = toolEntry("add_reaction")
	RunListReactions = toolEntry("list_reactions")
)
