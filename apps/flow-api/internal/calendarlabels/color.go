package calendarlabels

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// FallbackEventColor is the colour an event takes when neither a label nor
// its owner's membership supplies a valid one.
const FallbackEventColor = "#47B2F7"

// EventColor is an event's resolved colour and the label it came from.
type EventColor struct {
	Color         string
	OwnerColor    string
	LabelID       *types.PublicID
	LabelName     *string
	DetailsHidden bool
}

// ResolveEventColor mirrors nodate-time's rule: label colour, else the
// owner's member colour, else the fallback; a viewer who cannot see details
// gets the owner colour and no label.
//
// A colour is used only when it is "#" plus six hex digits, and is returned
// as stored (no case folding). Labelled means labelName.Valid.
func ResolveEventColor(detailsVisible bool, labelPublicID types.PublicID, labelName, labelColor, ownerMemberColor sql.NullString) EventColor {
	owner := FallbackEventColor
	if ownerMemberColor.Valid && isHexColor(ownerMemberColor.String) {
		owner = ownerMemberColor.String
	}
	out := EventColor{Color: owner, OwnerColor: owner}
	if !detailsVisible {
		out.DetailsHidden = true
		return out
	}
	if !labelName.Valid {
		return out
	}
	id, name := labelPublicID, labelName.String
	out.LabelID, out.LabelName = &id, &name
	if labelColor.Valid && isHexColor(labelColor.String) {
		out.Color = labelColor.String
	}
	return out
}

// isHexColor reports whether s matches ^#[0-9A-Fa-f]{6}$.
func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
