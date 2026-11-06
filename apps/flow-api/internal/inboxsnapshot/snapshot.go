// Package inboxsnapshot defines the bounded, actor-authorized input shared by
// AI triage and suggestion execution. The hash identifies exactly the
// projected source fields seen by the model plus link and freshness metadata.
package inboxsnapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/sourceintake"
)

const (
	// Version changes when the captured source shape or canonical JSON encoding
	// changes. Proposal events keep the opaque digest so commands can reject
	// recommendations based on an older source snapshot.
	Version = 1

	maxSourceBodyRunes = 1_000
)

// Fields is the raw, access-filtered signal projection used to construct a
// model input snapshot. Task fields must be supplied only after the caller has
// confirmed that the current actor may read the linked task.
type Fields struct {
	InboxItemID  string
	Source       string
	Kind         string
	Payload      []byte
	TaskPublicID string
	TaskTitle    string
	TaskReadable bool
	ReceivedAt   time.Time
	UpdatedAt    time.Time
}

// PromptItem is the bounded JSON object sent to the triage provider. It
// intentionally excludes the source URL and raw payload.
type PromptItem struct {
	InboxItemID string `json:"inboxItemId"`
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	SourceTitle string `json:"sourceTitle"`
	SourceBody  string `json:"sourceBody,omitempty"`
	TaskTitle   string `json:"taskTitle,omitempty"`
}

// Snapshot is the immutable captured input plus metadata used to validate the
// recommendation later. Task public id and timestamps affect the hash but are
// not sent to the provider.
type Snapshot struct {
	Version          int    `json:"version"`
	InboxItemID      string `json:"inboxItemId"`
	Source           string `json:"source"`
	Kind             string `json:"kind"`
	SourceTitle      string `json:"sourceTitle"`
	SourceBody       string `json:"sourceBody,omitempty"`
	TaskPublicID     string `json:"taskPublicId,omitempty"`
	TaskTitle        string `json:"taskTitle,omitempty"`
	ReceivedAtUnixMS int64  `json:"receivedAtUnixMs"`
	UpdatedAtUnixMS  int64  `json:"updatedAtUnixMs"`
}

// Capture projects and bounds the source fields, then records the linked task
// and source timestamps in the hash snapshot. The caller must pass task data
// only when current task ACL permits it.
func Capture(fields Fields) Snapshot {
	detail := sourceintake.Project(fields.Source, fields.Kind, fields.Payload)
	snapshot := Snapshot{
		Version:          Version,
		InboxItemID:      fields.InboxItemID,
		Source:           strings.ToLower(strings.TrimSpace(fields.Source)),
		Kind:             strings.TrimSpace(fields.Kind),
		SourceTitle:      detail.Title,
		SourceBody:       bodyExcerpt(detail.Body),
		ReceivedAtUnixMS: fields.ReceivedAt.UnixMilli(),
		UpdatedAtUnixMS:  fields.UpdatedAt.UnixMilli(),
	}
	if fields.TaskReadable {
		snapshot.TaskPublicID = strings.TrimSpace(fields.TaskPublicID)
		snapshot.TaskTitle = strings.TrimSpace(fields.TaskTitle)
	}
	return snapshot
}

// PromptItem returns only the fields included in the provider input.
func (s Snapshot) PromptItem() PromptItem {
	return PromptItem{
		InboxItemID: s.InboxItemID,
		Source:      s.Source,
		Kind:        s.Kind,
		SourceTitle: s.SourceTitle,
		SourceBody:  s.SourceBody,
		TaskTitle:   s.TaskTitle,
	}
}

// VersionHash returns the versioned canonical snapshot as lowercase hex
// SHA-256. Struct field order is fixed so the digest is stable across runs.
func (s Snapshot) VersionHash() string {
	encoded, err := json.Marshal(s)
	if err != nil {
		// All snapshot fields are JSON strings and integers, so marshal cannot
		// fail. Keep a stable zero digest if that assumption ever changes.
		return strings.Repeat("0", sha256.Size*2)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func bodyExcerpt(body string) string {
	if utf8.RuneCountInString(body) <= maxSourceBodyRunes {
		return body
	}
	cut := 0
	for at := range body {
		if cut == maxSourceBodyRunes {
			return body[:at]
		}
		cut++
	}
	return body
}
