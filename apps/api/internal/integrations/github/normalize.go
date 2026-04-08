package github

// NormalizeEventKind maps an inbound GitHub webhook event header
// (X-GitHub-Event) plus the JSON action field to a stable, canonical
// kind that we store in `signals.kind` (4.SIG-1). The output is
// dot-separated and lower-snake so it can be used as both a metric
// label and a constraint signal name.
//
// Examples:
//
//	pull_request + opened   → "pull_request.opened"
//	check_run    + completed → "check_run.completed"
//	deployment_status + ""  → "deployment_status"
//
// Unknown / missing event headers fall through as "unknown".
func NormalizeEventKind(eventHeader, action string) string {
	if eventHeader == "" {
		return "unknown"
	}
	if action == "" {
		return eventHeader
	}
	return eventHeader + "." + action
}

// KnownGithubKinds enumerates the github event kinds we currently
// handle in the constraint engine and timeline filters. The list is
// not exhaustive but covers the M4 acceptance scope (issues, pull
// requests, comments, check runs, deployments).
var KnownGithubKinds = []string{
	"issues.opened",
	"issues.closed",
	"issues.reopened",
	"issue_comment.created",
	"pull_request.opened",
	"pull_request.closed",
	"pull_request.merged",
	"pull_request.reopened",
	"pull_request_review.submitted",
	"check_run.completed",
	"check_suite.completed",
	"deployment",
	"deployment_status",
	"push",
}
