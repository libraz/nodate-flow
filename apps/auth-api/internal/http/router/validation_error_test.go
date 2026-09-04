package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postJSON drives a request through the real router and returns the
// status and the raw response body. Request validation runs before any
// handler touches the database, so the stub DB is never reached.
func postJSON(t *testing.T, path string, body string) (int, string) {
	t.Helper()
	res := BuildResult(stubDeps(t))
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	res.Handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestRefusedPasswordIsNotEchoed holds the boundary that a rejected
// credential does not come back in the refusal.
//
// The assertion is against the whole serialised response rather than a
// named member: a check on errors[0].value passes just as happily when
// the value reappears under detail, or in a message, or in a member
// added later.
func TestRefusedPasswordIsNotEchoed(t *testing.T) {
	t.Parallel()

	const submitted = "hunter2" // one short of the declared minimum
	status, body := postJSON(t, "/auth/register",
		`{"email":"someone@example.com","password":"`+submitted+`","displayName":"Someone"}`)

	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", status, body)
	}
	if strings.Contains(body, submitted) {
		t.Errorf("the refused password came back in the response body: %s", body)
	}
	// The refusal still has to be actionable.
	if !strings.Contains(body, "body.password") {
		t.Errorf("response does not name the field that was refused: %s", body)
	}
}

// TestRefusedOrdinaryFieldStillEchoesItsValue is the counterweight: the
// fix must drop the values that carry credentials, not every value. A
// model that suppressed the lot would pass the test above and fail here.
func TestRefusedOrdinaryFieldStillEchoesItsValue(t *testing.T) {
	t.Parallel()

	submitted := strings.Repeat("n", 101) // one over the declared maximum
	status, body := postJSON(t, "/auth/register",
		`{"email":"someone@example.com","password":"correct horse battery","displayName":"`+submitted+`"}`)

	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", status, body)
	}
	if !strings.Contains(body, "body.displayName") {
		t.Fatalf("response does not name the field that was refused: %s", body)
	}
	if !strings.Contains(body, submitted) {
		t.Errorf("the refused value was withheld for an ordinary field: %s", body)
	}
}

// TestMissingRequiredPropertyDoesNotEchoTheBody covers the shape that a
// per-field rule misses. Huma reports a missing required property
// against the enclosing object and echoes that object, so the refusal
// carried every sibling — the password among them — even though no
// individual field had failed.
func TestMissingRequiredPropertyDoesNotEchoTheBody(t *testing.T) {
	t.Parallel()

	const submitted = "correct horse battery staple"
	status, body := postJSON(t, "/auth/register",
		`{"password":"`+submitted+`","displayName":"Someone"}`)

	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", status, body)
	}
	if strings.Contains(body, submitted) {
		t.Errorf("the request body was echoed back with the password in it: %s", body)
	}
	if !strings.Contains(body, "email") {
		t.Errorf("response does not say which property was missing: %s", body)
	}
}

// TestMalformedBodyIsNotEchoed covers the parse-failure path, where the
// echoed value is the raw payload rather than a field of it.
func TestMalformedBodyIsNotEchoed(t *testing.T) {
	t.Parallel()

	const submitted = "correct horse battery staple"
	status, body := postJSON(t, "/auth/register",
		`{"email":"someone@example.com","password":"`+submitted+`"`)

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", status, body)
	}
	if strings.Contains(body, submitted) {
		t.Errorf("the raw request payload was echoed back: %s", body)
	}
	if !strings.Contains(body, `"location":"body"`) {
		t.Errorf("response does not locate the failure: %s", body)
	}
}

// TestRefusalStaysWellFormedProblemJSON guards the envelope itself: the
// sanitiser edits error details in place, and dropping a value must not
// leave a response the SDK cannot parse.
func TestRefusalStaysWellFormedProblemJSON(t *testing.T) {
	t.Parallel()

	status, body := postJSON(t, "/auth/register",
		`{"email":"someone@example.com","password":"short","displayName":"Someone"}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", status, body)
	}

	var envelope struct {
		Status int `json:"status"`
		Errors []struct {
			Message  string `json:"message"`
			Location string `json:"location"`
			Value    any    `json:"value"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("response is not valid JSON: %v; body = %s", err, body)
	}
	if envelope.Status != http.StatusUnprocessableEntity {
		t.Errorf("envelope status = %d, want 422", envelope.Status)
	}
	if len(envelope.Errors) == 0 {
		t.Fatalf("envelope carries no error details: %s", body)
	}
	for _, d := range envelope.Errors {
		if d.Message == "" || d.Location == "" {
			t.Errorf("error detail lost its message or location: %+v", d)
		}
	}
}
