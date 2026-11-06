package providers

import (
	"net"
	"net/url"
	"strings"
)

// OutputDialect is a provider-specific request shape whose support has been
// verified for an exact provider/model/endpoint combination.
type OutputDialect string

const (
	DialectOpenAIChatCompletions OutputDialect = "openai_chat_completions_json_schema"
	DialectAnthropicMessages     OutputDialect = "anthropic_messages_output_config_format"
	DialectOllamaGenerate        OutputDialect = "ollama_generate_schema_format"
)

// OutputCapability is the result of a pure native-schema capability lookup.
// Unknown means the caller must use validated text before making any request.
type OutputCapability struct {
	Verified bool
	Dialect  OutputDialect
	Reason   string
}

// NativeOutputCapability returns a verified dialect only for the small set of
// provider, model, and endpoint combinations backed by an explicit contract.
// It intentionally does not infer capability from model-name prefixes.
func NativeOutputCapability(kind Kind, model, endpoint string) OutputCapability {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	switch kind {
	case KindOpenAI:
		if endpoint != defaultOpenAIBaseURL {
			return OutputCapability{Reason: "non_official_endpoint"}
		}
		switch model {
		case "gpt-4o-mini", "gpt-4o-mini-2024-07-18", "gpt-4o-2024-08-06":
			return OutputCapability{Verified: true, Dialect: DialectOpenAIChatCompletions, Reason: "verified_model_snapshot"}
		default:
			return OutputCapability{Reason: "model_not_verified"}
		}
	case KindAnthropic:
		if endpoint != anthropicEndpoint {
			return OutputCapability{Reason: "non_official_endpoint"}
		}
		if model == "claude-opus-5-5" {
			return OutputCapability{Verified: true, Dialect: DialectAnthropicMessages, Reason: "verified_model"}
		}
		return OutputCapability{Reason: "model_not_verified"}
	case KindOllama:
		if loopbackOllamaEndpoint(endpoint) {
			return OutputCapability{Verified: true, Dialect: DialectOllamaGenerate, Reason: "loopback_daemon"}
		}
		return OutputCapability{Reason: "endpoint_not_loopback"}
	case KindGoogle:
		return OutputCapability{Reason: "legacy_gemini_contract_uses_validated_text"}
	case KindOpenAICompat:
		return OutputCapability{Reason: "compatible_endpoint_not_verified"}
	default:
		return OutputCapability{Reason: "provider_not_verified"}
	}
}

func loopbackOllamaEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Path != "" && u.Path != "/" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
