package asana

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type sleeps struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleeps) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
	return nil
}

func newTestClient(srv *httptest.Server) (*Client, *sleeps) {
	s := &sleeps{}
	return &Client{BaseURL: srv.URL, Token: "secret-token", HTTP: srv.Client(), Sleep: s.sleep}, s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func data(v any) map[string]any { return map[string]any{"data": v} }

func TestClientPagination(t *testing.T) {
	var auth, limits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		limits = append(limits, r.URL.Query().Get("limit"))
		switch r.URL.Query().Get("offset") {
		case "":
			writeJSON(w, 200, map[string]any{"data": []Tag{{GID: "1"}, {GID: "2"}}, "next_page": map[string]any{"offset": "o1"}})
		case "o1":
			writeJSON(w, 200, map[string]any{"data": []Tag{{GID: "3"}}, "next_page": nil})
		}
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	got, err := list[Tag](context.Background(), c, "/x", nil)
	if err != nil || len(got) != 3 {
		t.Fatalf("got %v err %v", got, err)
	}
	for i := range auth {
		if auth[i] != "Bearer secret-token" || limits[i] != "100" {
			t.Fatalf("request %d: auth %q limit %q", i, auth[i], limits[i])
		}
	}
}

func TestClientOffsetExpiry(t *testing.T) {
	run := func(expireTimes int) ([]Tag, error) {
		expired := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("offset") == "" {
				writeJSON(w, 200, map[string]any{"data": []Tag{{GID: "1"}}, "next_page": map[string]any{"offset": "o1"}})
				return
			}
			if expired < expireTimes {
				expired++
				writeJSON(w, 400, map[string]any{"errors": []map[string]string{{"message": "offset: Your pagination token has expired"}}})
				return
			}
			writeJSON(w, 200, map[string]any{"data": []Tag{{GID: "2"}}})
		}))
		defer srv.Close()
		c, _ := newTestClient(srv)
		got, err := list[Tag](context.Background(), c, "/x", nil)
		return got, err
	}
	if got, err := run(1); err != nil || len(got) != 2 {
		t.Fatalf("one expiry: got %v err %v", got, err)
	}
	if _, err := run(2); err == nil {
		t.Fatal("second expiry must fail")
	}
}

func TestClientRetryAfter(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		switch n {
		case 1:
			w.Header().Set("Retry-After", "7")
			writeJSON(w, 429, map[string]any{})
		case 2:
			writeJSON(w, 429, map[string]any{})
		case 3:
			writeJSON(w, 503, map[string]any{})
		case 4:
			writeJSON(w, 503, map[string]any{})
		default:
			writeJSON(w, 200, data([]Tag{}))
		}
	}))
	defer srv.Close()
	c, s := newTestClient(srv)
	c.Backoff = time.Second
	if _, err := list[Tag](context.Background(), c, "/x", nil); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{7 * time.Second, 60 * time.Second, time.Second, 2 * time.Second}
	if fmt.Sprint(s.d) != fmt.Sprint(want) {
		t.Fatalf("sleeps %v, want %v", s.d, want)
	}
}

func TestClientServerErrorGivesUp(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		writeJSON(w, 500, map[string]any{})
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	_, err := list[Tag](context.Background(), c, "/x", nil)
	if err == nil || n != 4 {
		t.Fatalf("calls %d err %v", n, err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatal("token leaked into error")
	}
}

func TestClientProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ok"):
			writeJSON(w, 200, data(map[string]string{"gid": "ok"}))
		case strings.HasSuffix(r.URL.Path, "/missing"):
			writeJSON(w, 404, map[string]any{})
		case strings.HasSuffix(r.URL.Path, "/denied"):
			writeJSON(w, 403, map[string]any{})
		default:
			writeJSON(w, 400, map[string]any{})
		}
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	ctx := context.Background()
	cases := map[string]ProbeResult{"ok": Exists, "missing": NotFound, "denied": Forbidden, "weird": Error}
	for gid, want := range cases {
		if got := c.ProbeTask(ctx, gid); got != want {
			t.Errorf("ProbeTask(%s) = %v, want %v", gid, got, want)
		}
		if got := c.ProbeStory(ctx, gid); got != want {
			t.Errorf("ProbeStory(%s) = %v, want %v", gid, got, want)
		}
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	c2, _ := newTestClient(dead)
	if got := c2.ProbeTask(ctx, "x"); got != Error {
		t.Errorf("transport failure = %v, want Error", got)
	}
}

func TestDownloadAttachment(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/attachments/9" {
			writeJSON(w, 200, data(Attachment{GID: "9", DownloadURL: srv.URL + "/blob"}))
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("download request carried Authorization")
		}
		_, _ = io.WriteString(w, "hello")
	}))
	defer srv.Close()
	c, _ := newTestClient(srv)
	var sb strings.Builder
	n, err := c.DownloadAttachment(context.Background(), "9", &sb)
	if err != nil || n != 5 || sb.String() != "hello" {
		t.Fatalf("n=%d body=%q err=%v", n, sb.String(), err)
	}
}
