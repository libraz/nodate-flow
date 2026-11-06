package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestInvocationAudienceDefaultsToInitiator(t *testing.T) {
	//nolint:staticcheck // SA1012: exercise the fail-closed audience fallback for an absent context.
	require.Equal(t, InvocationAudienceInitiator, InvocationAudienceFromContext(nil))
	require.Equal(t, InvocationAudienceInitiator, InvocationAudienceFromContext(context.Background()))
	require.Equal(t, InvocationAudienceInitiator, InvocationAudienceFromContext(
		WithInvocationAudience(context.Background(), "unrecognized"),
	))
}

func TestWithTaskIDDoesNotWidenInvocationAudience(t *testing.T) {
	ctx := WithTaskID(context.Background(), 42)
	require.Equal(t, uint32(42), TaskIDFromContext(ctx))
	require.Equal(t, InvocationAudienceInitiator, InvocationAudienceFromContext(ctx))

	ctx = WithInvocationAudience(ctx, InvocationAudienceTask)
	ctx = WithTaskID(ctx, 84)
	require.Equal(t, uint32(84), TaskIDFromContext(ctx))
	require.Equal(t, InvocationAudienceTask, InvocationAudienceFromContext(ctx))
}

func TestWithInputResourcesDeduplicatesPublicTaskReferences(t *testing.T) {
	parentID := uuid.NewString()
	childID := uuid.NewString()
	ctx := WithInputResources(context.Background(),
		TaskInputResource(parentID),
		TaskInputResource(strings.ToUpper(parentID)),
	)
	ctx = WithInputResources(ctx,
		TaskInputResource(childID),
		TaskInputResource(parentID),
	)

	resources := InputResourcesFromContext(ctx)
	require.Equal(t, []InvocationResource{
		TaskInputResource(parentID),
		TaskInputResource(childID),
	}, resources)
	resources[0].PublicID = "changed"
	require.Equal(t, parentID, InputResourcesFromContext(ctx)[0].PublicID,
		"readers receive copies and cannot mutate context provenance")
}

func TestWithInputResourcesPreservesUnknownReferencesForFailClosedRead(t *testing.T) {
	unknown := InvocationResource{Kind: "page", PublicID: uuid.NewString()}
	invalidTask := TaskInputResource("not-a-uuid")
	resources := InputResourcesFromContext(WithInputResources(context.Background(), unknown, invalidTask))
	require.Equal(t, []InvocationResource{unknown, invalidTask}, resources,
		"invalid provenance must not be silently dropped before the query can fail closed")
}
