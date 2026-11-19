package ai

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

func TestProposeStepsCacheKeyIncludesNormalizedInstruction(t *testing.T) {
	t.Parallel()

	vis := acl.VisibilityArgs{ActorUserID: 7}
	base := proposeStepsCacheKey(42, "Title", "Description", GranularityStandard, nil, vis, 99, false)
	empty := proposeStepsCacheKey(42, "Title", "Description", GranularityStandard, nil, vis, 99, false, "")
	first := proposeStepsCacheKey(42, "Title", "Description", GranularityStandard, nil, vis, 99, false, "focus on risks")
	trimmed := proposeStepsCacheKey(42, "Title", "Description", GranularityStandard, nil, vis, 99, false, "  focus on risks  ")
	second := proposeStepsCacheKey(42, "Title", "Description", GranularityStandard, nil, vis, 99, false, "focus on schedule")

	if base != empty {
		t.Fatal("an empty optional instruction changed the legacy cache key")
	}
	if first != trimmed {
		t.Fatal("cache keys for whitespace-equivalent instructions differ")
	}
	if first == second {
		t.Fatal("different instructions shared a propose-steps cache key")
	}
}

type instructionStepsProvider struct {
	mu       sync.Mutex
	requests []providers.Request
	response *providers.Response
}

func (p *instructionStepsProvider) Name() string         { return "instruction-steps" }
func (p *instructionStepsProvider) Kind() providers.Kind { return providers.KindOllama }
func (p *instructionStepsProvider) Model() string        { return "instruction-test-model" }
func (p *instructionStepsProvider) Complete(_ context.Context, req providers.Request) (*providers.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	return p.response, nil
}

func (p *instructionStepsProvider) recordedRequests() []providers.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]providers.Request(nil), p.requests...)
}

func TestProposeStepsInstructionIsUntrustedSeparateDataAndSkipsEmbeddings(t *testing.T) {
	t.Parallel()

	const response = `[
		{"title":"Step 1","description":"d","priority":"low"},
		{"title":"Step 2","description":"d","priority":"low"},
		{"title":"Step 3","description":"d","priority":"low"},
		{"title":"Step 4","description":"d","priority":"low"},
		{"title":"Step 5","description":"d","priority":"low"}
	]`
	provider := &instructionStepsProvider{
		response: &providers.Response{
			Termination: providers.TerminationComplete,
			Model:       "instruction-test-model",
			Text:        response,
		},
	}
	embedProvider := &stubEmbedProvider{}
	orchestrator := &Orchestrator{
		Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return provider, nil
		}),
		ProposalCache: NewProposalCache(time.Minute),
	}
	reader := emptySmartCreateReader{}
	embedClient := embed.New(embedProvider, stubEmbedStore{})

	call := func(instruction string) error {
		_, err := orchestrator.ProposeSteps(
			context.Background(), 42,
			"Launch the release", "The original saved task description.",
			GranularityStandard, nil,
			embedClient, reader, acl.VisibilityArgs{}, instruction,
		)
		return err
	}

	if err := call("  focus on operational risks  "); err != nil {
		t.Fatalf("first proposal: %v", err)
	}
	if err := call("focus on operational risks"); err != nil {
		t.Fatalf("normalized equivalent proposal: %v", err)
	}
	if err := call("focus on the launch sequence"); err != nil {
		t.Fatalf("different instruction proposal: %v", err)
	}

	requests := provider.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("provider calls = %d, want one cached call for each distinct instruction", len(requests))
	}
	for _, req := range requests {
		if req.MaxTokens != stepsInstructionMaxTokens {
			t.Errorf("instruction MaxTokens = %d, want bounded cap %d", req.MaxTokens, stepsInstructionMaxTokens)
		}
		if !strings.Contains(req.System, "untrusted, task-scoped input") {
			t.Errorf("system prompt does not mark the instruction untrusted: %q", req.System)
		}
		if !strings.Contains(req.Prompt, "Description:\nThe original saved task description.") {
			t.Errorf("task description is missing from the task field: %q", req.Prompt)
		}
		if !strings.Contains(req.Prompt, "## Optional User Instruction (Untrusted Caller Input)") {
			t.Errorf("instruction is not in a separate untrusted prompt section: %q", req.Prompt)
		}
		if !strings.Contains(req.Prompt, `{"instruction":"`) {
			t.Errorf("instruction is not encoded as a separate JSON data field: %q", req.Prompt)
		}
	}
	if got := embedProvider.calls; got != 0 {
		t.Errorf("embedding calls = %d, want 0 for instructed proposals", got)
	}
	if strings.Contains(requests[0].Prompt, "focus on the launch sequence") || strings.Contains(requests[1].Prompt, "focus on operational risks") {
		t.Fatal("distinct instructions were mixed between cached prompts")
	}
}

func TestProposeStepsRejectsTooLongInstruction(t *testing.T) {
	t.Parallel()

	orchestrator := &Orchestrator{
		Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
			return nil, errors.New("resolver must not be called")
		}),
	}
	_, err := orchestrator.ProposeSteps(
		context.Background(), 42,
		"Task", "Description", GranularityStandard, nil,
		nil, nil, acl.VisibilityArgs{}, strings.Repeat("界", MaxStepsInstructionRunes+1),
	)
	if !errors.Is(err, ErrStepsInstructionTooLong) {
		t.Fatalf("error = %v, want ErrStepsInstructionTooLong", err)
	}
}
