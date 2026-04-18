package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProfileUpdateDisplayName verifies that a user can update their
// display name via PATCH /me and the change is persisted.
func TestProfileUpdateDisplayName(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Update display name.
	var patched struct {
		DisplayName string `json:"displayName"`
	}
	doJSON(t, http.MethodPatch, testServerURL+"/me", tt.AccessToken,
		map[string]any{"displayName": "New Display Name"}, &patched)
	require.Equal(t, "New Display Name", patched.DisplayName)

	// Verify it persists on re-read.
	var me struct {
		DisplayName string `json:"displayName"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me", tt.AccessToken, nil, &me)
	require.Equal(t, "New Display Name", me.DisplayName)
}

// TestProfileUpdateLocale verifies that a user can change their locale
// preference.
func TestProfileUpdateLocale(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var patched struct {
		Locale string `json:"locale"`
	}
	doJSON(t, http.MethodPatch, testServerURL+"/me", tt.AccessToken,
		map[string]any{"locale": "ja"}, &patched)
	require.Equal(t, "ja", patched.Locale)

	var me struct {
		Locale string `json:"locale"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me", tt.AccessToken, nil, &me)
	require.Equal(t, "ja", me.Locale)
}

// TestProfileUpdateTheme verifies that a user can change their theme
// preference.
func TestProfileUpdateTheme(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var patched struct {
		ThemePreference string `json:"themePreference"`
	}
	doJSON(t, http.MethodPatch, testServerURL+"/me", tt.AccessToken,
		map[string]any{"themePreference": "aurora-dark"}, &patched)
	require.Equal(t, "aurora-dark", patched.ThemePreference)
}

// TestProfileGetReturnsRegisteredEmail verifies that GET /me returns
// the email used during registration.
func TestProfileGetReturnsRegisteredEmail(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var me struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me", tt.AccessToken, nil, &me)
	require.Equal(t, tt.Email, me.Email)
	require.Equal(t, tt.UserPublicID, me.ID)
}
