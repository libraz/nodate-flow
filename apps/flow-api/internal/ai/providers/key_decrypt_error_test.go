package providers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// errSealedKeyMismatch stands in for the one thing that actually breaks
// decryption in production: the ciphertext in ai_providers was sealed
// with a different NF_SECRET_KEY than the process is running with.
var errSealedKeyMismatch = errors.New("crypto: message authentication failed")

// brokenDecryptor fails every unseal, the way every provider row in a
// deployment does after the secret key is rotated without a re-seal.
type brokenDecryptor struct{}

func (brokenDecryptor) Decrypt([]byte) ([]byte, error) { return nil, errSealedKeyMismatch }

// TestUndecryptableKeyIsNotReportedAsAnUnreachableUpstream is the guard
// on what an operator is told when the key cannot be unsealed.
//
// Nothing is sent upstream in that case, and nothing can be: the failure
// is local, total across every provider in the deployment, and permanent
// until the key is fixed. Reported as a transport failure it points at
// the network and the vendor's status page, which is where the answer is
// not — and it is the answer that survives every retry.
//
// The check is per provider kind, because the decrypt step is written out
// once in each of them and it is the sentinel, not the message, that the
// error code is chosen from.
func TestUndecryptableKeyIsNotReportedAsAnUnreachableUpstream(t *testing.T) {
	for _, kind := range []Kind{KindAnthropic, KindGoogle, KindOpenAICompat} {
		t.Run(string(kind), func(t *testing.T) {
			// A live server that would answer happily, so a passing test
			// cannot be explained by the upstream being unavailable.
			var reached atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached.Store(true)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			p, err := New(Config{
				Kind:         kind,
				Name:         "test-" + string(kind),
				BaseURL:      srv.URL,
				EncryptedKey: []byte("sealed-with-a-key-we-no-longer-have"),
			}, brokenDecryptor{})
			if err != nil {
				t.Fatalf("New(%s): %v", kind, err)
			}

			_, err = p.Complete(context.Background(), Request{Prompt: "hello"})
			if err == nil {
				t.Fatal("a provider whose key cannot be unsealed must fail")
			}
			if !errors.Is(err, ErrKeyDecryptFailed) {
				t.Errorf("want ErrKeyDecryptFailed, got %v", err)
			}
			if errors.Is(err, ErrUpstreamUnreachable) || errors.Is(err, ErrUpstreamTimeout) {
				t.Errorf("an unsealable key is not a reachability problem: %v", err)
			}
			if !errors.Is(err, errSealedKeyMismatch) {
				t.Errorf("the underlying cause must stay in the chain for the log: %v", err)
			}
			if reached.Load() {
				t.Error("no request may go upstream when the key could not be unsealed")
			}
		})
	}
}
