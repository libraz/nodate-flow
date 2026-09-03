package auth

// Floor is the minimum role a caller has to hold before a mutating request
// may reach the operations sitting behind it.
//
// The vocabulary lives here rather than next to the HTTP router because more
// than one transport has to answer the same question. The router groups its
// routes by floor; the MCP tool table decides the same thing per tool. A
// second, transport-local enum would let the two drift, and a change that is
// allowed over one transport and refused over the other is an authorization
// hole shaped like a naming difference.
type Floor string

const (
	// FloorNone marks a surface that mounts no role floor of its own. Its
	// authorization lives somewhere the surface cannot see, and the place it
	// lives has to be named — see Enforcement.
	FloorNone Floor = ""
	// FloorWorkspaceMember keeps guests, the read-only workspace role, out of
	// mutations while leaving their reads alone.
	FloorWorkspaceMember Floor = "workspace:member"
	// FloorWorkspaceAdmin restricts the whole surface to workspace admins and
	// owners, reads included.
	FloorWorkspaceAdmin Floor = "workspace:admin"
	// FloorProjectCommenter admits the conversational project roles:
	// commenters and above.
	FloorProjectCommenter Floor = "project:commenter"
	// FloorProjectEditor admits the structural project roles: editors and
	// above.
	FloorProjectEditor Floor = "project:editor"
	// FloorProjectLead admits only the role that decides who else reaches
	// the project. Editing a project's contents and deciding its membership
	// are different powers: an editor who could also grant roles could
	// promote themselves and remove everyone above them.
	FloorProjectLead Floor = "project:lead"
)

// Floors returns every declared floor. Callers that enumerate floors — the
// tests that prove each one is enforced, tooling that renders the role matrix
// — read them from here, so a floor added to the vocabulary and forgotten
// everywhere else shows up as an uncovered entry rather than as silence.
func Floors() []Floor {
	return []Floor{
		FloorNone,
		FloorWorkspaceMember,
		FloorWorkspaceAdmin,
		FloorProjectCommenter,
		FloorProjectEditor,
		FloorProjectLead,
	}
}

// floorRank orders the floors by how few callers they admit, so two
// surfaces answering for the same operation can be compared.
//
// The ladder is a single chain even though it mixes workspace and project
// roles, because each rung admits a subset of the one below it: a project
// commenter is a workspace member with a project role on top, and a
// workspace admin is auto-elevated past every project floor, so the set of
// callers a workspace-admin surface admits sits inside the set a
// project-lead surface admits.
var floorRank = map[Floor]int{
	FloorNone:             0,
	FloorWorkspaceMember:  1,
	FloorProjectCommenter: 2,
	FloorProjectEditor:    3,
	FloorProjectLead:      4,
	FloorWorkspaceAdmin:   5,
}

// AtLeast reports whether f admits no more callers than other does.
//
// It is what lets one transport be held to another's decision: a tool that
// answers for a REST operation may demand the same floor or a stricter one,
// and anything below it is a way in that the other transport refuses.
func (f Floor) AtLeast(other Floor) bool {
	return floorRank[f] >= floorRank[other]
}

// Enforcement names the kind of evidence that authorises a mutation running
// without a role floor.
//
// It exists because "checked elsewhere" is not a claim anything can verify.
// Each kind corresponds to a place a static check can look: a function the
// handler has to reach, middleware the route group has to mount, or a
// statement that has to bind every row it changes to the caller. An
// exemption records the kind together with the concrete names, so an
// exemption whose check was deleted stops matching the source.
type Enforcement string

const (
	// EnforcedByHandlerCall means the registered handler itself calls the
	// named check, and refuses the request when it fails.
	EnforcedByHandlerCall Enforcement = "handler-call"
	// EnforcedByGroupMiddleware means the route group mounts the named
	// middleware ahead of the handler.
	EnforcedByGroupMiddleware Enforcement = "group-middleware"
	// EnforcedByActorScopedWrite means there is no shared state to protect:
	// every statement the operation runs binds its rows to the caller, so the
	// request cannot change what any other member sees.
	EnforcedByActorScopedWrite Enforcement = "actor-scoped-write"
)
