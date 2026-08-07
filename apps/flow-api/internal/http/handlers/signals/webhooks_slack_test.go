package signals

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	sl "github.com/libraz/nodate-flow/apps/flow-api/internal/integrations/slack"
)

// testSlackSecret is the signing secret the handshake tests configure on
// Deps and sign their requests with.
const testSlackSecret = "test-slack-signing-secret"

// signedSlackRequest builds a POST /webhooks/slack request carrying a
// valid v0 signature for body.
func signedSlackRequest(body []byte) *http.Request {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/slack", bytes.NewReader(body))
	req.Header.Set(sl.TimestampHeader, ts)
	req.Header.Set(sl.SignatureHeader, sl.Sign(body, ts, testSlackSecret))
	return req
}

// TestSlackURLVerificationEchoesChallenge locks in the Events API
// handshake: Slack only saves a Request URL when the endpoint answers
// {"type":"url_verification","challenge":"..."} with 200 and the same
// challenge value. Without it the Slack app cannot be configured at all,
// which makes every other branch of the handler unreachable in a real
// deployment.
//
// Deps carries no DB and no DefaultWorkspaceID on purpose: the handshake
// happens at app-configuration time and must not depend on either.
func TestSlackURLVerificationEchoesChallenge(t *testing.T) {
	body := []byte(`{"token":"Jhj5dZrVaK7ZwHHjRyZWjbDl","challenge":"3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P","type":"url_verification"}`)

	rec := httptest.NewRecorder()
	HandleSlackWebhook(Deps{SlackSigningSecret: testSlackSecret})(rec, signedSlackRequest(body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", err, rec.Body.String())
	}
	const want = "3eZbrw1aBm2rZgRNFdxV2595E9CY3gmdALWMmHkvFXO7tYXAYM8P"
	if got.Challenge != want {
		t.Fatalf("challenge = %q; want %q", got.Challenge, want)
	}
}

// TestSlackURLVerificationRequiresSignature asserts the handshake is
// answered only after signature verification. Slack signs the url_
// verification request like every other delivery, so handling it before
// the check would hand an unauthenticated caller a working oracle for
// this endpoint.
func TestSlackURLVerificationRequiresSignature(t *testing.T) {
	body := []byte(`{"challenge":"unsigned-probe-challenge","type":"url_verification"}`)

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/slack", bytes.NewReader(body))
	req.Header.Set(sl.TimestampHeader, ts)
	req.Header.Set(sl.SignatureHeader, sl.Sign(body, ts, "a-different-signing-secret"))

	rec := httptest.NewRecorder()
	HandleSlackWebhook(Deps{SlackSigningSecret: testSlackSecret})(rec, req)

	if rec.Code != apierrors.IntegrationSlackWebhookSignatureMismatch.Status {
		t.Fatalf("status = %d; want %d\nbody: %s",
			rec.Code, apierrors.IntegrationSlackWebhookSignatureMismatch.Status, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "unsigned-probe-challenge") {
		t.Fatalf("rejected request echoed the challenge: %s", rec.Body.String())
	}
}

// TestSlackURLVerificationRejectsEmptyChallenge covers the malformed
// handshake: a url_verification envelope with nothing to echo is not
// something we can answer, and returning 200 with an empty challenge
// would let Slack record the URL as verified when it is not.
func TestSlackURLVerificationRejectsEmptyChallenge(t *testing.T) {
	body := []byte(`{"type":"url_verification"}`)

	rec := httptest.NewRecorder()
	HandleSlackWebhook(Deps{SlackSigningSecret: testSlackSecret})(rec, signedSlackRequest(body))

	if rec.Code != apierrors.IntegrationSlackWebhookPayloadUnparseable.Status {
		t.Fatalf("status = %d; want %d\nbody: %s",
			rec.Code, apierrors.IntegrationSlackWebhookPayloadUnparseable.Status, rec.Body.String())
	}
}
