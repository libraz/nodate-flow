package gateway

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscordgoSyncEventsDispatchesHandlersInline(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/bwmarrin/discordgo")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("locate discordgo module: %v", err)
	}
	eventSrc, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(out)), "event.go"))
	if err != nil {
		t.Fatalf("read discordgo event.go: %v", err)
	}
	src := string(eventSrc)
	for _, want := range []string{
		"if s.SyncEvents {\n\t\t\teh.eventHandler.Handle(s, i)\n\t\t} else {\n\t\t\tgo eh.eventHandler.Handle(s, i)\n\t\t}",
		"if s.SyncEvents {\n\t\t\t\teh.eventHandler.Handle(s, i)\n\t\t\t} else {\n\t\t\t\tgo eh.eventHandler.Handle(s, i)\n\t\t\t}",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("discordgo event dispatch no longer matches SyncEvents inline contract; missing:\n%s", want)
		}
	}
}
