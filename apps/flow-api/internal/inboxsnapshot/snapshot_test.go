package inboxsnapshot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCaptureBoundsProjectedSourceAndOmitsRawPayloadAndURL(t *testing.T) {
	t.Parallel()

	fields := Fields{
		InboxItemID: "019a1000-0000-7000-8000-000000000001",
		Source:      "manual",
		Kind:        "manual",
		Payload: []byte(`{"title":"` + strings.Repeat("題", 600) + `","body":"` + strings.Repeat("文", 1200) +
			`","sourceUrl":"https://private.example.test/source"}`),
		ReceivedAt: time.UnixMilli(1_700_000_000_100),
		UpdatedAt:  time.UnixMilli(1_700_000_001_200),
	}

	snapshot := Capture(fields)
	if got := len([]rune(snapshot.SourceTitle)); got != 500 {
		t.Fatalf("source title rune count = %d, want 500", got)
	}
	if got := len([]rune(snapshot.SourceBody)); got != maxSourceBodyRunes {
		t.Fatalf("source body rune count = %d, want %d", got, maxSourceBodyRunes)
	}
	encoded, err := json.Marshal(snapshot.PromptItem())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sourceUrl") || strings.Contains(string(encoded), "private.example.test") {
		t.Fatalf("provider input contains a URL: %s", encoded)
	}
	if len(snapshot.VersionHash()) != 64 {
		t.Fatalf("version hash length = %d, want 64", len(snapshot.VersionHash()))
	}
}

func TestVersionHashCoversReadableLinkAndSourceTimestamps(t *testing.T) {
	t.Parallel()

	base := Fields{
		InboxItemID:  "019a1000-0000-7000-8000-000000000001",
		Source:       "manual",
		Kind:         "manual",
		Payload:      []byte(`{"title":"Title","body":"Body"}`),
		TaskPublicID: "019a1000-0000-7000-8000-000000000002",
		TaskTitle:    "Task title",
		TaskReadable: true,
		ReceivedAt:   time.UnixMilli(1_700_000_000_100),
		UpdatedAt:    time.UnixMilli(1_700_000_001_200),
	}
	baseHash := Capture(base).VersionHash()

	changedLink := base
	changedLink.TaskPublicID = "019a1000-0000-7000-8000-000000000003"
	if Capture(changedLink).VersionHash() == baseHash {
		t.Fatal("changing the readable task link did not change source version")
	}
	changedSource := base
	changedSource.UpdatedAt = changedSource.UpdatedAt.Add(time.Millisecond)
	if Capture(changedSource).VersionHash() == baseHash {
		t.Fatal("changing source updated_at did not change source version")
	}
}

func TestCaptureDoesNotExposeOrHashUnreadableTaskReference(t *testing.T) {
	t.Parallel()

	base := Fields{
		InboxItemID:  "019a1000-0000-7000-8000-000000000001",
		Source:       "manual",
		Kind:         "manual",
		Payload:      []byte(`{"title":"Title","body":"Body"}`),
		TaskPublicID: "019a1000-0000-7000-8000-000000000002",
		TaskTitle:    "Private task title",
		TaskReadable: false,
		ReceivedAt:   time.UnixMilli(1_700_000_000_100),
		UpdatedAt:    time.UnixMilli(1_700_000_001_200),
	}
	first := Capture(base)
	secondInput := base
	secondInput.TaskPublicID = "019a1000-0000-7000-8000-000000000003"
	secondInput.TaskTitle = "Other private title"
	second := Capture(secondInput)

	if first.TaskPublicID != "" || first.TaskTitle != "" {
		t.Fatalf("unreadable task leaked into captured snapshot: %+v", first)
	}
	if first.VersionHash() != second.VersionHash() {
		t.Fatal("unreadable task data changed the actor's source version")
	}
}
