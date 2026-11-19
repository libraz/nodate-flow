package ai

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/airequest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

type onDemandGroup struct {
	mu      sync.Mutex
	running map[string]*onDemandCall
}

type onDemandCall struct {
	done    chan struct{}
	payload []byte
	err     error
}

// GenerateOnDemand returns a validated JSON proposal from at most one
// provider call. The caller must establish access and include the actor
// and all current input facts in scope or prompt. The caller checks that
// the inputs are still authorized before exposing the result.
func (o *Orchestrator) GenerateOnDemand(
	ctx context.Context,
	workspaceID uint32,
	purpose, scope, system, prompt string,
	spec *providers.OutputSpec,
	maxTokens int,
	validate func([]byte) error,
) ([]byte, error) {
	if o == nil || o.Resolver == nil {
		return nil, ErrNoProvider
	}
	if spec == nil || scope == "" || maxTokens <= 0 {
		return nil, providers.ErrResponseSchemaMismatch
	}
	key := ProposalCacheKey(workspaceID, "on-demand", purpose, scope, system, prompt,
		spec.Contract(), string(spec.SchemaJSON()), strconv.Itoa(maxTokens))
	if data, ok := o.ProposalCache.Get(key); ok {
		if payload, ok := data.(json.RawMessage); ok {
			return append([]byte(nil), payload...), nil
		}
	}
	group := &o.onDemand
	group.mu.Lock()
	if call := group.running[key]; call != nil {
		group.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
			return append([]byte(nil), call.payload...), call.err
		}
	}
	if group.running == nil {
		group.running = make(map[string]*onDemandCall)
	}
	call := &onDemandCall{done: make(chan struct{}), err: ErrParse}
	group.running[key] = call
	group.mu.Unlock()
	defer func() {
		group.mu.Lock()
		delete(group.running, key)
		close(call.done)
		group.mu.Unlock()
	}()
	// Another call can finish between the first cache check and acquiring
	// the group lock. Reuse the result it just published.
	if data, ok := o.ProposalCache.Get(key); ok {
		if payload, ok := data.(json.RawMessage); ok {
			call.payload, call.err = payload, nil
			return append([]byte(nil), payload...), nil
		}
	}
	if _, err := CheckBudget(ctx, o.Guard, workspaceID); err != nil {
		call.err = err
		return nil, err
	}
	provider, err := o.Resolver.Default(ctx, workspaceID)
	if err != nil {
		call.err = err
		return nil, err
	}
	if provider == nil {
		call.err = ErrNoProvider
		return nil, call.err
	}
	ctx = providers.WithSingleAttempt(providers.WithWorkspaceID(ctx, workspaceID))
	request := airequest.New(provider, airequest.Args{
		System: system, Prompt: prompt, Output: spec, MaxTokens: maxTokens,
	})
	var payload []byte
	_, call.err = o.CompleteAndRecord(ctx, workspaceID, purpose, provider, request, func(response *providers.Response) error {
		var err error
		payload, err = outputPayload(response, spec)
		if err != nil {
			return err
		}
		if validate != nil {
			return validate(payload)
		}
		return nil
	})
	if call.err != nil {
		return nil, call.err
	}
	call.payload = payload
	o.ProposalCache.Put(key, json.RawMessage(payload))
	return append([]byte(nil), payload...), nil
}
