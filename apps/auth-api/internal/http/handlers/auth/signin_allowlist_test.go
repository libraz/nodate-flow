package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
)

func domainRow(value string) generated.ListEnabledOauthSigninAllowlistEntriesRow {
	return generated.ListEnabledOauthSigninAllowlistEntriesRow{
		EntryKind:  generated.OauthSigninAllowlistEntryKindDomain,
		EntryValue: value,
	}
}

func emailRow(value string) generated.ListEnabledOauthSigninAllowlistEntriesRow {
	return generated.ListEnabledOauthSigninAllowlistEntriesRow{
		EntryKind:  generated.OauthSigninAllowlistEntryKindEmail,
		EntryValue: value,
	}
}

// admits runs the decision the way the sign-in path runs it: the
// environment entries and the enabled rows are unioned first, and the
// address is matched against the union.
func admits(envDomains, envEmails []string, rows []generated.ListEnabledOauthSigninAllowlistEntriesRow, email string) bool {
	domains, emails := signInAllowlistUnion(envDomains, envEmails, rows)
	return signInAllowlistAdmits(domains, emails, email)
}

// TestSignInAllowlist_Admits covers who the allowlist lets in. The
// environment-only rows state the default that must survive this table
// having a database half at all: an unconfigured instance stays open, and
// the matching rule (exact address, exact apex domain, case- and
// whitespace-insensitive, malformed refused once restricted) is the same
// whichever side an entry came from.
func TestSignInAllowlist_Admits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		domains []string
		emails  []string
		rows    []generated.ListEnabledOauthSigninAllowlistEntriesRow
		email   string
		want    bool
	}{
		{"nothing configured anywhere allows anyone", nil, nil, nil, "anyone@anywhere.test", true},
		{"nothing configured anywhere allows even malformed", nil, nil, nil, "not-an-email", true},
		{"env domain match", []string{"example.com"}, nil, nil, "alice@example.com", true},
		{"env domain non-match", []string{"example.com"}, nil, nil, "alice@other.com", false},
		{"env exact email match", nil, []string{"vip@vendor.test"}, nil, "vip@vendor.test", true},
		{"env exact email non-match", nil, []string{"vip@vendor.test"}, nil, "other@vendor.test", false},
		{"domain OR email — email branch", []string{"example.com"}, []string{"contractor@vendor.test"}, nil, "contractor@vendor.test", true},
		{"domain OR email — domain branch", []string{"example.com"}, []string{"contractor@vendor.test"}, nil, "bob@example.com", true},
		{"domain OR email — neither", []string{"example.com"}, []string{"contractor@vendor.test"}, nil, "eve@evil.test", false},
		{"case-insensitive domain", []string{"example.com"}, nil, nil, "Alice@EXAMPLE.CoM", true},
		{"case-insensitive exact email", nil, []string{"vip@vendor.test"}, nil, "VIP@Vendor.Test", true},
		{"whitespace around email", []string{"example.com"}, nil, nil, "  alice@example.com  ", true},
		{"malformed no at rejected when restricted", []string{"example.com"}, nil, nil, "no-at-sign", false},
		{"trailing at rejected when restricted", []string{"example.com"}, nil, nil, "alice@", false},
		{"subdomain is not the apex domain", []string{"example.com"}, nil, nil, "alice@sub.example.com", false},

		{"row domain match", nil, nil, []generated.ListEnabledOauthSigninAllowlistEntriesRow{domainRow("example.com")}, "alice@example.com", true},
		{"row domain non-match", nil, nil, []generated.ListEnabledOauthSigninAllowlistEntriesRow{domainRow("example.com")}, "alice@other.com", false},
		{"row exact email match", nil, nil, []generated.ListEnabledOauthSigninAllowlistEntriesRow{emailRow("vip@vendor.test")}, "vip@vendor.test", true},
		{"row exact email non-match", nil, nil, []generated.ListEnabledOauthSigninAllowlistEntriesRow{emailRow("vip@vendor.test")}, "other@vendor.test", false},
		{"row malformed no at rejected when restricted", nil, nil, []generated.ListEnabledOauthSigninAllowlistEntriesRow{domainRow("example.com")}, "no-at-sign", false},
		{"row subdomain is not the apex domain", nil, nil, []generated.ListEnabledOauthSigninAllowlistEntriesRow{domainRow("example.com")}, "alice@sub.example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, admits(tt.domains, tt.emails, tt.rows, tt.email))
		})
	}
}

// TestSignInAllowlist_RowsAddToTheEnvironment pins the union: the first
// row an administrator adds must not turn the configured entries off. A
// rule that replaced the environment with the database would lock out the
// operator's own domain the moment anyone used the admin screen.
func TestSignInAllowlist_RowsAddToTheEnvironment(t *testing.T) {
	t.Parallel()
	env := []string{"example.com"}
	rows := []generated.ListEnabledOauthSigninAllowlistEntriesRow{domainRow("vendor.test")}

	assert.True(t, admits(env, nil, rows, "alice@example.com"),
		"a row must not take away what the environment grants")
	assert.True(t, admits(env, nil, rows, "bob@vendor.test"),
		"a row must add to what the environment grants")
	assert.False(t, admits(env, nil, rows, "eve@evil.test"),
		"neither side names this domain")
}

// TestSignInAllowlist_NormalizesRowValues covers entry_value arriving
// exactly as stored: the column is latin1_bin, so a row written through
// any path keeps its casing, padding, and leading "@" byte for byte, and
// only normalization in Go makes it comparable to an address.
func TestSignInAllowlist_NormalizesRowValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		row   generated.ListEnabledOauthSigninAllowlistEntriesRow
		email string
	}{
		{"upper-case domain row", domainRow("Example.COM"), "alice@example.com"},
		{"domain row written with a leading at", domainRow("@example.com"), "alice@example.com"},
		{"padded domain row", domainRow("  example.com  "), "alice@example.com"},
		{"upper-case email row", emailRow("VIP@Vendor.Test"), "vip@vendor.test"},
		{"padded email row", emailRow(" vip@vendor.test "), "vip@vendor.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows := []generated.ListEnabledOauthSigninAllowlistEntriesRow{tt.row}
			assert.True(t, admits(nil, nil, rows, tt.email),
				"a row must match regardless of how its bytes were stored")
		})
	}
}

// TestSignInAllowlist_BlankRowDoesNotRestrict asserts a row carrying no
// value leaves the instance open rather than closing it. Counted as an
// entry, a blank value would make an empty allowlist look active and
// refuse every address while matching none.
func TestSignInAllowlist_BlankRowDoesNotRestrict(t *testing.T) {
	t.Parallel()
	rows := []generated.ListEnabledOauthSigninAllowlistEntriesRow{domainRow("   "), emailRow("")}
	assert.True(t, admits(nil, nil, rows, "anyone@anywhere.test"))
}

// TestSignInAllowlist_IgnoresUnknownRowKind asserts a value whose kind is
// not one this code knows admits nobody. Read as either kind it would
// widen access on a guess.
func TestSignInAllowlist_IgnoresUnknownRowKind(t *testing.T) {
	t.Parallel()
	rows := []generated.ListEnabledOauthSigninAllowlistEntriesRow{
		{EntryKind: generated.OauthSigninAllowlistEntryKind("ip_range"), EntryValue: "example.com"},
		domainRow("vendor.test"),
	}
	assert.False(t, admits(nil, nil, rows, "alice@example.com"),
		"an unreadable entry kind must not admit its value")
	assert.True(t, admits(nil, nil, rows, "bob@vendor.test"),
		"the entries alongside it still apply")
}

// TestSignInAllowlist_DoesNotWriteIntoTheConfiguredSlices guards the
// concurrency hazard in building the union: the configured slices are
// shared by every request, so appending onto them would let one sign-in
// write into another's allowlist.
func TestSignInAllowlist_DoesNotWriteIntoTheConfiguredSlices(t *testing.T) {
	t.Parallel()
	envDomains := make([]string, 1, 8)
	envDomains[0] = "example.com"
	envEmails := make([]string, 1, 8)
	envEmails[0] = "vip@vendor.test"
	rows := []generated.ListEnabledOauthSigninAllowlistEntriesRow{
		domainRow("added.test"),
		emailRow("added@vendor.test"),
	}

	domains, emails := signInAllowlistUnion(envDomains, envEmails, rows)

	require.Len(t, domains, 2)
	require.Len(t, emails, 2)
	assert.Equal(t, []string{"example.com"}, envDomains[:1])
	assert.Equal(t, []string{"vip@vendor.test"}, envEmails[:1])
	assert.NotSame(t, &envDomains[0], &domains[0],
		"the union must be built in its own backing array")
	assert.NotSame(t, &envEmails[0], &emails[0],
		"the union must be built in its own backing array")
}
