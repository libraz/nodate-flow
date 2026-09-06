package eventkinds

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/payloadscan"
)

// TestNoInternalIDsInEventPayloads type-checks every package that builds
// an event payload and fails on an id-shaped field whose value is not a
// string.
//
// The runtime rail in eventlog.Append is exact but only fires on a path a
// test drives; this covers the builders no test reaches. It reads types
// rather than source text on purpose. A source scan cannot tell a live
// call from a commented-out one, and it cannot tell `"taskId": in.TaskID`
// where TaskID is a UUID string from the same line where it is a uint32 —
// so it produces false positives, the false positives go into an
// allowlist, and the allowlist is where the next real leak hides.
func TestNoInternalIDsInEventPayloads(t *testing.T) {
	root := moduleRoot(t)
	dirs := payloadPackages(t, filepath.Join(root, "internal"))
	if len(dirs) == 0 {
		t.Fatal("no payload-building packages found; the scan would prove nothing")
	}

	// One cache for every package below. Each is type-checked
	// independently, but they import much the same things, and an import
	// resolved a second time costs another `go list` subprocess rather
	// than a map lookup.
	cache := payloadscan.NewExportCache()
	if err := cache.Warm(root); err != nil {
		t.Fatalf("warm export cache: %v", err)
	}

	// The packages are still walked concurrently: what remains after the
	// cache is type-checking, and that is work the checker will happily
	// do on as many cores as it is given.
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		sem  = make(chan struct{}, runtime.GOMAXPROCS(0))
		fail []string
	)
	for _, dir := range dirs {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			rel, err := filepath.Rel(root, dir)
			if err != nil {
				rel = dir
			}
			findings, err := payloadscan.Scan(payloadscan.Config{Dir: dir, Cache: cache})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fail = append(fail, fmt.Sprintf("scan %s: %v", rel, err))
				return
			}
			for _, f := range findings {
				fail = append(fail, fmt.Sprintf(
					"%s: event payload field %q is %s, not a string — every identifier on the wire is a public_id (UUID v7); resolve it before building the payload (%s)",
					rel, f.Key, f.Type, f.Pos))
			}
		}(dir)
	}
	wg.Wait()

	sort.Strings(fail)
	for _, msg := range fail {
		t.Error(msg)
	}
}

// moduleRoot returns the directory holding the module's go.mod, walking
// up from the test's working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// payloadPackages lists the package directories under root that assign an
// event payload. Discovering them by walking the tree means a new
// package is scanned the day it is added, instead of the day someone
// remembers to add it to a list here.
func payloadPackages(t *testing.T, root string) []string {
	t.Helper()
	// The walk only collects candidate paths; the files are read after it
	// returns, so no filesystem operation runs inside the callback.
	var candidates []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Generated query code has no hand-written payloads.
			if info.Name() == "generated" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			candidates = append(candidates, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	seen := map[string]struct{}{}
	for _, path := range candidates {
		src, rerr := os.ReadFile(filepath.Clean(path))
		if rerr != nil {
			t.Fatalf("read %s: %v", path, rerr)
		}
		if strings.Contains(string(src), "Payload:") || strings.Contains(string(src), "ExtraPayload:") {
			seen[filepath.Dir(path)] = struct{}{}
		}
	}
	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}
