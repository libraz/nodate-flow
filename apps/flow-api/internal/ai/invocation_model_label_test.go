package ai

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// labelProvider records the request it was handed and answers with a
// canned response. Its Model is what a resolved workspace provider would
// report before any call is made.
type labelProvider struct {
	model string
	resp  *providers.Response
	err   error
	req   providers.Request
}

func (p *labelProvider) Name() string         { return "label" }
func (p *labelProvider) Kind() providers.Kind { return providers.Kind("labelkind") }
func (p *labelProvider) Model() string        { return p.model }
func (p *labelProvider) Complete(_ context.Context, req providers.Request) (*providers.Response, error) {
	p.req = req
	if p.err != nil {
		return nil, p.err
	}
	return p.resp, nil
}

type sample struct {
	provider string
	model    string
	ws       string
	cost     int64
	err      error
}

// TestInvocationMetricsCarryAModelLabel covers the metrics half of the
// request-plumbing defect. Every orchestrator call site built its
// request without naming a model and then labelled the Prometheus
// invocation and cost samples with that same empty field, so the panels
// showed spend the operator could not attribute to any model.
//
// Both branches are asserted because they degrade differently: on
// success there is a response to read the model off, but on failure
// there is not, and failures are exactly when an operator goes looking.
func TestInvocationMetricsCarryAModelLabel(t *testing.T) {
	t.Parallel()

	const wantModel = "claude-sonnet-4-6"

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		prov := &labelProvider{
			model: wantModel,
			resp:  &providers.Response{Model: wantModel, Text: `[{"title":"t","description":"d","priority":"low"}]`},
		}
		var got []sample
		o := &Orchestrator{
			Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
				return prov, nil
			}),
			OnInvocation: func(provider, model, ws string, cost int64, err error) {
				got = append(got, sample{provider, model, ws, cost, err})
			},
		}
		if _, err := o.ProposeTasksFrom(context.Background(), 42, "ship the thing"); err != nil {
			t.Fatalf("ProposeTasksFrom: %v", err)
		}
		requireOneLabelledSample(t, got, wantModel, "42")
		if prov.req.Model != wantModel {
			t.Errorf("the request itself must name the model too, got %q", prov.req.Model)
		}
	})

	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		prov := &labelProvider{model: wantModel, err: errors.New("upstream exploded")}
		var got []sample
		o := &Orchestrator{
			Resolver: ProviderResolverFunc(func(context.Context, uint32) (providers.Provider, error) {
				return prov, nil
			}),
			OnInvocation: func(provider, model, ws string, cost int64, err error) {
				got = append(got, sample{provider, model, ws, cost, err})
			},
		}
		if _, err := o.ProposeTasksFrom(context.Background(), 42, "ship the thing"); err == nil {
			t.Fatal("expected the provider error to surface")
		}
		requireOneLabelledSample(t, got, wantModel, "42")
	})
}

func requireOneLabelledSample(t *testing.T, got []sample, wantModel, wantWS string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("expected exactly one metrics sample, got %d: %+v", len(got), got)
	}
	s := got[0]
	if s.model == "" {
		t.Error("the model label is empty; AI cost and invocation metrics would be unattributable")
	}
	if s.model != wantModel {
		t.Errorf("model label = %q, want %q", s.model, wantModel)
	}
	if s.provider == "" {
		t.Error("the provider label is empty")
	}
	if s.ws != wantWS {
		t.Errorf("workspace label = %q, want %q", s.ws, wantWS)
	}
}

// TestAgentExecutorAppliesTheAgentsOwnSettings pins the wiring the task
// agent depends on. The executor loads its row through the concrete
// generated.Queries handle, so there is no seam to inject a fake row
// without a database; what is asserted here is that the row it loaded is
// the thing the request is built from.
//
// Read the guarantee narrowly: it fixes only that the call is still
// present in the source. What ForAgent and FromExecRow then produce is
// covered directly by the airequest package's own tests, and the whole
// path end to end by the signal-judge runner tests, whose agent lookup
// is an interface.
func TestAgentExecutorAppliesTheAgentsOwnSettings(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("agent_executor.go")
	if err != nil {
		t.Fatalf("read agent_executor.go: %v", err)
	}
	for _, want := range []string{"airequest.ForAgent(", "airequest.FromExecRow(row)"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("the agent executor must build its request with %s; "+
				"without it the agent's configured model, output cap, and temperature never reach the provider "+
				"and the run silently bills the workspace default model instead", want)
		}
	}
}
