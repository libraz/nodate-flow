package ai

import (
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

func TestAgentTemperature(t *testing.T) {
	t.Parallel()
	u := func(v uint16) *uint16 { return &v }
	cases := []struct {
		name string
		in   *uint16
		kind providers.Kind
		want uint16
		ok   bool
	}{
		{"absent takes the default", nil, providers.KindOpenAI, 100, true},
		{"explicit zero is kept", u(0), providers.KindOpenAI, 0, true},
		{"explicit zero on anthropic", u(0), providers.KindAnthropic, 0, true},
		{"openai accepts 200", u(200), providers.KindOpenAI, 200, true},
		{"anthropic accepts 100", u(100), providers.KindAnthropic, 100, true},
		{"anthropic refuses above 100", u(101), providers.KindAnthropic, 0, false},
	}
	for _, tc := range cases {
		got, ok := agentTemperature(tc.in, tc.kind)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestValidEventTriggerTypes(t *testing.T) {
	t.Parallel()
	if !validEventTriggerTypes(nil) {
		t.Error("an empty list clears the triggers and must be valid")
	}
	if !validEventTriggerTypes([]string{string(eventbus.Kinds()[0])}) {
		t.Error("a declared kind must be valid")
	}
	if validEventTriggerTypes([]string{"not.a.kind"}) {
		t.Error("an undeclared kind must be refused")
	}
}
