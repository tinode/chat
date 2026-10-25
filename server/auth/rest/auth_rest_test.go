package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store/types"
)

type testTransport func(*http.Request) (*http.Response, error)

// RoundTrip handles an HTTP request without opening a network connection.
func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestGenSecretDeadline verifies the serialized deadline and rejects provider violations.
func TestGenSecretDeadline(t *testing.T) {
	for _, name := range []string{"honored", "extended", "missing expiry", "expired token", "expired record", "ordinary token"} {
		t.Run(name, func(t *testing.T) {
			deadline := time.Now().Add(time.Minute).Truncate(time.Second)
			expiry := deadline
			switch name {
			case "extended":
				expiry = deadline.Add(time.Hour)
			case "missing expiry":
				expiry = time.Time{}
			case "expired token":
				expiry = time.Now().Add(-time.Second)
			case "expired record":
				deadline = time.Now().Add(-time.Second)
			case "ordinary token":
				deadline = time.Time{}
				expiry = time.Now().Add(time.Hour)
			}
			calls := 0
			a := &authenticator{name: "rest", serverUrl: "http://provider/", httpClient: &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var req request
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				if req.Endpoint != "gen" || req.Record == nil || !req.Record.ExpiresAt.Equal(deadline) {
					t.Errorf("deadline lost in REST request: %+v", req.Record)
				}
				body, err := json.Marshal(response{ByteVal: []byte("token"), TimeVal: expiry})
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			secret, got, err := a.GenSecret(&auth.Rec{ExpiresAt: deadline, Features: auth.FeatureNoLogin})
			if name == "honored" || name == "ordinary token" {
				if err != nil || string(secret) != "token" || !got.Equal(expiry) {
					t.Fatalf("valid issuance failed: %v %v", got, err)
				}
			} else if !errors.Is(err, types.ErrExpired) || len(secret) != 0 || !got.IsZero() {
				t.Fatalf("unsafe issuance returned: %q %v %v", secret, got, err)
			}
			if name == "expired record" && calls != 0 {
				t.Fatal("expired record reached provider")
			}
		})
	}
}
