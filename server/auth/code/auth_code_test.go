package code

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// Model persistent storage without a TTL: correctness must not depend on GC.
type testCodeCache map[string]string

func (c testCodeCache) Get(key string) (string, error) {
	if value, ok := c[key]; ok {
		return value, nil
	}
	return "", types.ErrNotFound
}
func (c testCodeCache) Upsert(key, value string, failOnDuplicate bool) error {
	if _, ok := c[key]; ok && failOnDuplicate {
		return types.ErrDuplicate
	}
	c[key] = value
	return nil
}
func (c testCodeCache) Delete(key string) error        { delete(c, key); return nil }
func (c testCodeCache) Expire(string, time.Time) error { return nil }

func setupCodeTest(t *testing.T) (*authenticator, testCodeCache) {
	t.Helper()
	old := store.PCache
	t.Cleanup(func() { store.PCache = old })
	cache := testCodeCache{}
	store.PCache = cache
	ca := &authenticator{}
	if err := ca.Init([]byte(`{"code_length":6,"expire_in":900,"max_retries":3}`), "code"); err != nil {
		t.Fatal(err)
	}
	return ca, cache
}

func TestCodeAuthenticationEnforcesExpiration(t *testing.T) {
	for _, tc := range []struct {
		name, deadline string
		want           error
	}{
		{"future", strconv.FormatInt(time.Now().Add(time.Hour).UnixNano(), 10), nil},
		{"expired", strconv.FormatInt(time.Now().Add(-time.Hour).UnixNano(), 10), types.ErrExpired},
		{"invalid expiration", "invalid", types.ErrFailed},
		{"legacy entry", "", types.ErrFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ca, cache := setupCodeTest(t)
			uid := types.Uid(123)
			value := "123456:0:" + uid.String()
			if tc.deadline != "" {
				value += ":" + tc.deadline
			}
			cache["code_email:alice@example.com"] = value
			rec, _, err := ca.Authenticate([]byte("123456:email:alice@example.com"), "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if tc.want != nil {
				if rec != nil {
					t.Fatal("returned auth record for invalid code")
				}
				return
			}
			if rec == nil || rec.Uid != uid || rec.Features&auth.FeatureNoLogin == 0 {
				t.Fatalf("unexpected record: %+v", rec)
			}
			if _, _, err := ca.Authenticate([]byte("123456:email:alice@example.com"), ""); !errors.Is(err, types.ErrFailed) {
				t.Fatalf("code replay accepted: %v", err)
			}
		})
	}
}

func TestCodeGenerationPersistsCustomExpiration(t *testing.T) {
	ca, cache := setupCodeTest(t)
	code, expiry, err := ca.GenSecret(&auth.Rec{Uid: types.Uid(123), Credential: "email:alice@example.com", Lifetime: auth.Duration(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Fatalf("invalid code length: %d", len(code))
	}
	parts := strings.Split(cache["code_email:alice@example.com"], ":")
	if len(parts) != 4 || parts[0] != string(code) || parts[3] != strconv.FormatInt(expiry.UnixNano(), 10) {
		t.Fatalf("missing persisted deadline: %v", parts)
	}
	if remaining := time.Until(expiry); remaining <= 0 || remaining > time.Minute+time.Second {
		t.Fatalf("custom lifetime ignored: %v", remaining)
	}
	// Make the persisted code expired without generating any other code or running GC.
	parts[3] = strconv.FormatInt(time.Now().Add(-time.Second).UnixNano(), 10)
	cache["code_email:alice@example.com"] = strings.Join(parts, ":")
	if _, _, err := ca.Authenticate([]byte(string(code)+":email:alice@example.com"), ""); !errors.Is(err, types.ErrExpired) {
		t.Fatalf("expired generated code accepted: %v", err)
	}
}

func TestCodeRetriesPreserveExpiration(t *testing.T) {
	ca, cache := setupCodeTest(t)
	deadline := strconv.FormatInt(time.Now().Add(time.Minute).UnixNano(), 10)
	key := "code_email:alice@example.com"
	cache[key] = "123456:0:" + types.Uid(123).String() + ":" + deadline
	for attempt := 1; attempt <= 3; attempt++ {
		if _, _, err := ca.Authenticate([]byte("654321:email:alice@example.com"), ""); !errors.Is(err, types.ErrFailed) {
			t.Fatalf("wrong code accepted: %v", err)
		}
		parts := strings.Split(cache[key], ":")
		if parts[1] != strconv.Itoa(attempt) || parts[3] != deadline {
			t.Fatalf("retry changed expiration or lost counter: %v", parts)
		}
	}
	if _, _, err := ca.Authenticate([]byte("123456:email:alice@example.com"), ""); !errors.Is(err, types.ErrFailed) {
		t.Fatalf("retry limit bypassed: %v", err)
	}
}
