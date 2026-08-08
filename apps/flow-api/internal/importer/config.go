package importer

import (
	"errors"
	"fmt"
	"strings"
)

// import_jobs.config_json is a plaintext JSON column. It is not
// encrypted, it is returned by the job-read endpoints, and it is dumped
// with the rest of the row in a backup — so whatever is written into it
// is stored the way it was typed. A free-form blob under a field the UI
// labels "Configuration" is an invitation to paste a personal access
// token into it, and for github / jira / linear there is no other place
// in the product to put one, which makes that the obvious thing to try.
//
// The choice was between encrypting the column and refusing to store
// the values that would need encrypting. This refuses them, because
// there is nothing yet that could use a stored credential: the worker
// implements csv only and fails every other source before it reads the
// configuration. Encrypting would add a key-management surface, a
// rotation story, and a decrypt path to guard, all to protect a value
// no connector reads. It would also be the more dangerous half-measure
// — a column that looks safe invites more secrets into it, and the day
// the connectors land they would arrive already holding credentials
// under whatever scheme was guessed at now.
//
// So the contract is: config_json holds source-specific, non-secret
// settings, enforced by an allow-list per source. When a connector is
// written it declares its own keys here, and its credentials go where
// the other integrations' already do — user_integrations / ai_providers,
// which are AES-256-GCM encrypted.

// allowedConfigKeys is the set of top-level keys a source with a
// connector accepts. A source absent from this map has not declared its
// keys yet, so only the credential rule applies to it.
//
// The gap is deliberate. github / jira / linear have no importer behind
// them, and a job created for one of them is meant to reach the worker
// and fail with "not implemented" — that is the difference between
// "we cannot do this yet" and a progress bar that never moves, and it
// is a behaviour the suite locks in. Refusing their settings at create
// would take that answer away to protect a shape nobody has defined.
// What must not reach the column either way is a credential, and that
// rule holds for every source.
var allowedConfigKeys = map[string]map[string]bool{
	"csv": {"csv": true},
}

// credentialKeySubstrings match a key name that names a secret. Matched
// against the key lowercased with separators removed, so apiKey,
// api_key and API-KEY all read as "apikey".
var credentialKeySubstrings = []string{
	"accesskey",
	"apikey",
	"authorization",
	"bearer",
	"clientsecret",
	"cookie",
	"credential",
	"passphrase",
	"passwd",
	"password",
	"privatekey",
	"secret",
	"sessionkey",
	"token",
}

// credentialKeyExact match only as whole key names. They are too short
// to use as substrings — "pat" is inside "path", "key" inside "keyword"
// — and a validator that rejects a legitimate key is as much a defect
// as one that admits a secret.
var credentialKeyExact = map[string]bool{
	"auth": true,
	"key":  true,
	"pass": true,
	"pat":  true,
	"pwd":  true,
}

// ErrConfigKeyUnknown reports a key the source does not define.
var ErrConfigKeyUnknown = errors.New("importer: unknown configuration key")

// ErrConfigKeySecret reports a key whose name says it carries a
// credential. It is separate from ErrConfigKeyUnknown so the caller can
// tell the user which of the two problems it has — "this source has no
// such setting" and "do not put a token here" need different answers.
var ErrConfigKeySecret = errors.New("importer: configuration must not carry credentials")

// ConfigError names the offending key alongside the reason.
type ConfigError struct {
	Key string
	Err error
}

func (e *ConfigError) Error() string { return fmt.Sprintf("%v: %s", e.Err, e.Key) }
func (e *ConfigError) Unwrap() error { return e.Err }

// ValidateConfig checks an import job's configuration before it is
// stored.
//
// The credential check runs over the whole structure, not just its top
// level, because nesting is the first thing anyone tries once a flat key
// is refused. It also runs first: a secret buried under an unrecognised
// key is still a secret, and "unknown key" would send the caller off to
// fix the wrong thing. The allow-list then applies to top-level keys,
// which is where a source's settings live.
func ValidateConfig(source string, cfg map[string]any) error {
	if len(cfg) == 0 {
		return nil
	}
	if err := findNestedCredential(cfg); err != nil {
		return err
	}
	allowed, declared := allowedConfigKeys[source]
	if !declared {
		return nil
	}
	for key := range cfg {
		if !allowed[key] {
			return &ConfigError{Key: key, Err: ErrConfigKeyUnknown}
		}
	}
	return nil
}

func findNestedCredential(v any) error {
	switch t := v.(type) {
	case map[string]any:
		for key, val := range t {
			if isCredentialKey(key) {
				return &ConfigError{Key: key, Err: ErrConfigKeySecret}
			}
			if err := findNestedCredential(val); err != nil {
				return err
			}
		}
	case []any:
		for _, val := range t {
			if err := findNestedCredential(val); err != nil {
				return err
			}
		}
	}
	return nil
}

// isCredentialKey reports whether a key name says the value is a secret.
func isCredentialKey(key string) bool {
	norm := normalizeKey(key)
	if credentialKeyExact[norm] {
		return true
	}
	for _, s := range credentialKeySubstrings {
		if strings.Contains(norm, s) {
			return true
		}
	}
	return false
}

// normalizeKey lowercases a key and drops everything that is not a
// letter or digit, so the separator style a caller happens to use
// cannot slip a name past the match.
func normalizeKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range strings.ToLower(key) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
