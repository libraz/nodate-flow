package calendarlabels

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func TestResolveEventColor(t *testing.T) {
	pub := types.New()
	none := sql.NullString{}
	cases := []struct {
		name        string
		visible     bool
		labelName   sql.NullString
		labelColor  sql.NullString
		owner       sql.NullString
		wantColor   string
		wantOwner   string
		wantLabeled bool
		wantHidden  bool
	}{
		{"label colour wins", true, ns("Work"), ns("#112233"), ns("#AABBCC"), "#112233", "#AABBCC", true, false},
		{"lowercase label colour kept as stored", true, ns("Work"), ns("#aabbcc"), ns("#445566"), "#aabbcc", "#445566", true, false},
		{"invalid label colour falls to owner", true, ns("Work"), ns("red"), ns("#445566"), "#445566", "#445566", true, false},
		{"short label colour is invalid", true, ns("Work"), ns("#fff"), ns("#445566"), "#445566", "#445566", true, false},
		{"NULL label colour falls to owner", true, ns("Work"), none, ns("#445566"), "#445566", "#445566", true, false},
		{"invalid label colour and NULL owner", true, ns("Work"), ns("nope"), none, FallbackEventColor, FallbackEventColor, true, false},
		{"no label uses owner", true, none, none, ns("#445566"), "#445566", "#445566", false, false},
		{"no label, NULL owner", true, none, none, none, FallbackEventColor, FallbackEventColor, false, false},
		{"no label, empty owner", true, none, none, ns(""), FallbackEventColor, FallbackEventColor, false, false},
		{"no label, invalid owner", true, none, none, ns("red"), FallbackEventColor, FallbackEventColor, false, false},
		{"no label, short owner", true, none, none, ns("#fff"), FallbackEventColor, FallbackEventColor, false, false},
		{"no label, non-hex owner", true, none, none, ns("#GGGGGG"), FallbackEventColor, FallbackEventColor, false, false},
		{"hidden drops the label", false, ns("Work"), ns("#112233"), ns("#445566"), "#445566", "#445566", false, true},
		{"hidden without label", false, none, none, ns("#445566"), "#445566", "#445566", false, true},
		{"hidden with invalid owner", false, ns("Work"), ns("#112233"), ns(""), FallbackEventColor, FallbackEventColor, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveEventColor(tc.visible, pub, tc.labelName, tc.labelColor, tc.owner)
			require.Equal(t, tc.wantColor, got.Color)
			require.Equal(t, tc.wantOwner, got.OwnerColor)
			require.Equal(t, tc.wantHidden, got.DetailsHidden)
			if tc.wantLabeled {
				require.NotNil(t, got.LabelID)
				require.Equal(t, pub, *got.LabelID)
				require.Equal(t, tc.labelName.String, *got.LabelName)
			} else {
				require.Nil(t, got.LabelID)
				require.Nil(t, got.LabelName)
			}
		})
	}
}
