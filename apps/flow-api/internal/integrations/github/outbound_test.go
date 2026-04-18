package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateIssueCommentRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer t0k3n" {
			t.Fatalf("missing/incorrect auth header")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42}`))
	}))
	defer srv.Close()

	c := &OutboundClient{Token: "t0k3n", BaseURL: srv.URL, HTTP: srv.Client()}
	id, err := c.CreateIssueComment(context.Background(), "o", "r", 7, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("expected id 42, got %d", id)
	}
}

func TestCreateIssueCommentUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := &OutboundClient{Token: "x", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.CreateIssueComment(context.Background(), "o", "r", 1, ""); err == nil {
		t.Fatal("expected error")
	}
}
