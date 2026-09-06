package config

import (
	"strings"
	"testing"
	"time"
)

// TestValidateEnumsRejectsShortSignalToken pins the boot-time floor on
// NF_FLOW_API_SIGNAL_TOKEN.
//
// The token is the entire authorization for POST /signals' machine path
// and the /internal/* group: no session, no membership, no workspace
// scoping behind it. Empty has to stay valid because empty is how the
// feature is turned off, which leaves exactly one thing to check — that
// a value somebody meant as a secret is long enough to be one.
func TestValidateEnumsRejectsShortSignalToken(t *testing.T) {
	t.Parallel()

	base := Config{
		Port:              "8080",
		AgentRunner:       "log",
		AgentQueueBackend: "memory",
		AgentTickInterval: time.Minute,
		WebhooksInsecure:  true,
	}

	longEnough := strings.Repeat("a", minSignalTokenLen)
	generated := strings.Repeat("0123456789abcdef", 4) // openssl rand -hex 32

	cases := []struct {
		name     string
		token    string
		accepted bool
	}{
		{"unset disables the service-token routes", "", true},
		{"whitespace only reads as unset", "   ", true},
		{"a single character", "x", false},
		{"one short of the floor", strings.Repeat("a", minSignalTokenLen-1), false},
		{"padded to look long", "  " + strings.Repeat("a", minSignalTokenLen-1) + "  ", false},
		{"exactly the floor", longEnough, true},
		{"as generated", generated, true},
		{"surrounded by whitespace but long enough", " " + generated + " ", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base
			cfg.FlowAPISignalToken = tc.token
			err := validateEnums(&cfg)
			if tc.accepted {
				if err != nil {
					t.Fatalf("NF_FLOW_API_SIGNAL_TOKEN=%q should boot: %v", tc.token, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("NF_FLOW_API_SIGNAL_TOKEN=%q opens both service-token surfaces behind a guessable secret and must fail the boot", tc.token)
			}
			if !strings.Contains(err.Error(), "NF_FLOW_API_SIGNAL_TOKEN") {
				t.Errorf("the error must name the variable the operator has to fix, got: %v", err)
			}
		})
	}
}

// TestSignalTokenFloorMatchesDocumentedGeneration keeps the floor
// reachable by the command the deployment files tell operators to run.
// A floor above what `openssl rand -hex 32` produces would reject the
// only token anybody was told to make.
func TestSignalTokenFloorMatchesDocumentedGeneration(t *testing.T) {
	t.Parallel()

	const hex32Len = 64
	if minSignalTokenLen > hex32Len {
		t.Fatalf("the floor is %d characters, but the documented `openssl rand -hex 32` yields %d", minSignalTokenLen, hex32Len)
	}
	if minSignalTokenLen < 16 {
		t.Fatalf("a floor of %d characters admits a secret that can be guessed inside the global rate limit", minSignalTokenLen)
	}
}
