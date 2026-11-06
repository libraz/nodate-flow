package ai

import (
	"context"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// InvocationAudience describes the default reader group for an AI invocation
// after every referenced task and the subject task have passed current ACL
// checks. Empty and unrecognized values resolve to the private initiator scope.
type InvocationAudience string

const (
	InvocationAudienceInitiator InvocationAudience = "initiator"
	InvocationAudienceTask      InvocationAudience = "task"
	InvocationAudienceWorkspace InvocationAudience = "workspace"
)

// InvocationResource is a public reference to content included in a provider
// input. Internal database identifiers must never be written here.
type InvocationResource struct {
	Kind     string `json:"kind"`
	PublicID string `json:"publicId"`
}

type invocationAudienceContextKey struct{}
type invocationResourcesContextKey struct{}

func normalizeInvocationAudience(audience InvocationAudience) InvocationAudience {
	switch audience {
	case InvocationAudienceTask, InvocationAudienceWorkspace, InvocationAudienceInitiator:
		return audience
	default:
		return InvocationAudienceInitiator
	}
}

// TaskInputResource builds provenance for a task without accepting an
// internal row id. The reader query rejects unknown resource kinds and UUIDs.
func TaskInputResource(publicID string) InvocationResource {
	return InvocationResource{Kind: "task", PublicID: strings.TrimSpace(publicID)}
}

// WithInvocationAudience explicitly widens or narrows the row's audience.
// WithTaskID remains a subject-task tag and never changes this value.
func WithInvocationAudience(ctx context.Context, audience InvocationAudience) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationAudienceContextKey{}, normalizeInvocationAudience(audience))
}

// InvocationAudienceFromContext returns the safely restricted default when
// no caller explicitly assigned an audience.
func InvocationAudienceFromContext(ctx context.Context) InvocationAudience {
	if ctx == nil {
		return InvocationAudienceInitiator
	}
	audience, _ := ctx.Value(invocationAudienceContextKey{}).(InvocationAudience)
	return normalizeInvocationAudience(audience)
}

// WithInputResources adds public task references used to build the provider
// input. Repeated references are de-duplicated without discarding malformed
// entries; the SQL reader policy fails closed on an unknown or invalid ref.
func WithInputResources(ctx context.Context, resources ...InvocationResource) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	all := append(InputResourcesFromContext(ctx), resources...)
	return context.WithValue(ctx, invocationResourcesContextKey{}, uniqueInvocationResources(all))
}

// InputResourcesFromContext returns a copy so downstream callers cannot
// mutate provenance already attached to an upstream context.
func InputResourcesFromContext(ctx context.Context) []InvocationResource {
	if ctx == nil {
		return nil
	}
	resources, _ := ctx.Value(invocationResourcesContextKey{}).([]InvocationResource)
	return append([]InvocationResource(nil), resources...)
}

// uniqueInvocationResources preserves input order while collapsing duplicate
// public references. Valid task UUIDs are canonicalized before comparison.
func uniqueInvocationResources(resources []InvocationResource) []InvocationResource {
	seen := make(map[string]struct{}, len(resources))
	unique := make([]InvocationResource, 0, len(resources))
	for _, resource := range resources {
		resource.Kind = strings.TrimSpace(resource.Kind)
		resource.PublicID = strings.TrimSpace(resource.PublicID)
		if resource.Kind == "task" {
			if publicID, err := types.Parse(resource.PublicID); err == nil {
				resource.PublicID = publicID.String()
			}
		}
		key := resource.Kind + "\x00" + resource.PublicID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, resource)
	}
	return unique
}
