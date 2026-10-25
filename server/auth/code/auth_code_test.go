package code

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/server/auth"
	_ "github.com/tinode/chat/server/auth/token"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
)

// testCodeCache models atomic persistent storage without expiration cleanup.
type testCodeCache struct {
	mu     sync.Mutex
	values map[string]string
}

// Get reads a cached record.
func (c *testCodeCache) Get(key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if value, ok := c.values[key]; ok {
		return value, nil
	}
	return "", types.ErrNotFound
}

// Upsert creates a record or replaces it when duplicates are permitted.
func (c *testCodeCache) Upsert(key, value string, failOnDuplicate bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.values[key]; ok && failOnDuplicate {
		return types.ErrDuplicate
	}
	c.values[key] = value
	return nil
}

// CompareAndSwap replaces a record only if its current value matches.
func (c *testCodeCache) CompareAndSwap(key, oldValue, newValue string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if value, ok := c.values[key]; !ok || value != oldValue {
		return false, nil
	}
	c.values[key] = newValue
	return true, nil
}

// CompareAndDelete consumes a record only if its current value matches.
func (c *testCodeCache) CompareAndDelete(key, value string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if current, ok := c.values[key]; !ok || current != value {
		return false, nil
	}
	delete(c.values, key)
	return true, nil
}

// Delete unconditionally removes a cached record.
func (c *testCodeCache) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.values, key)
	return nil
}

// Expire deliberately leaves records in place to test validity independently of GC.
func (c *testCodeCache) Expire(string, time.Time) error { return nil }

func setupCodeTest(t *testing.T) (*authenticator, *testCodeCache) {
	t.Helper()
	old := store.PCache
	t.Cleanup(func() { store.PCache = old })
	oldInfo, oldWarn, oldErr := logs.Info, logs.Warn, logs.Err
	logs.Init(io.Discard, "stdFlags")
	t.Cleanup(func() { logs.Info, logs.Warn, logs.Err = oldInfo, oldWarn, oldErr })
	cache := &testCodeCache{values: make(map[string]string)}
	store.PCache = cache
	ca := &authenticator{}
	if err := ca.Init([]byte(`{"code_length":6,"expire_in":900,"max_retries":3}`), "code"); err != nil {
		t.Fatal(err)
	}
	return ca, cache
}

// TestCodeAuthenticationEnforcesExpiration checks validity and rejects code replay.
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
			cache.values["code_email:alice@example.com"] = value
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

// TestCodeGenerationPersistsCustomExpiration checks that issuance persists its deadline.
func TestCodeGenerationPersistsCustomExpiration(t *testing.T) {
	ca, cache := setupCodeTest(t)
	code, expiry, err := ca.GenSecret(&auth.Rec{Uid: types.Uid(123), Credential: "email:alice@example.com", Lifetime: auth.Duration(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Fatalf("invalid code length: %d", len(code))
	}
	parts := strings.Split(cache.values["code_email:alice@example.com"], ":")
	if len(parts) != 4 || parts[0] != string(code) || parts[3] != strconv.FormatInt(expiry.UnixNano(), 10) {
		t.Fatalf("missing persisted deadline: %v", parts)
	}
	if remaining := time.Until(expiry); remaining <= 0 || remaining > time.Minute+time.Second {
		t.Fatalf("custom lifetime ignored: %v", remaining)
	}
	// Make the persisted code expired without generating any other code or running GC.
	parts[3] = strconv.FormatInt(time.Now().Add(-time.Second).UnixNano(), 10)
	cache.values["code_email:alice@example.com"] = strings.Join(parts, ":")
	if _, _, err := ca.Authenticate([]byte(string(code)+":email:alice@example.com"), ""); !errors.Is(err, types.ErrExpired) {
		t.Fatalf("expired generated code accepted: %v", err)
	}
}

// TestCodeRetriesPreserveExpiration checks that retries cannot extend the deadline.
func TestCodeRetriesPreserveExpiration(t *testing.T) {
	ca, cache := setupCodeTest(t)
	deadline := strconv.FormatInt(time.Now().Add(time.Minute).UnixNano(), 10)
	key := "code_email:alice@example.com"
	cache.values[key] = "123456:0:" + types.Uid(123).String() + ":" + deadline
	for attempt := 1; attempt <= 3; attempt++ {
		if _, _, err := ca.Authenticate([]byte("654321:email:alice@example.com"), ""); !errors.Is(err, types.ErrFailed) {
			t.Fatalf("wrong code accepted: %v", err)
		}
		parts := strings.Split(cache.values[key], ":")
		if parts[1] != strconv.Itoa(attempt) || parts[3] != deadline {
			t.Fatalf("retry changed expiration or lost counter: %v", parts)
		}
	}
	if _, _, err := ca.Authenticate([]byte("123456:email:alice@example.com"), ""); !errors.Is(err, types.ErrFailed) {
		t.Fatalf("retry limit bypassed: %v", err)
	}
}

// TestCodeAuthenticationLifetimeAfterDeletion checks the post-storage deadline boundary.
func TestCodeAuthenticationLifetimeAfterDeletion(t *testing.T) {
	for _, delay := range []time.Duration{30 * time.Second, 59 * time.Second, 59500 * time.Millisecond, time.Minute, time.Minute + time.Nanosecond} {
		t.Run(delay.String(), func(t *testing.T) {
			ca, _ := setupCodeTest(t)
			now := time.Now().Truncate(time.Second)
			ca.now = func() time.Time { return now }
			expiry := now.Add(time.Minute)
			value := "123456:0:" + types.Uid(123).String() + ":" + strconv.FormatInt(expiry.UnixNano(), 10)
			cache := mock_store.NewMockPersistentCacheInterface(gomock.NewController(t))
			store.PCache = cache
			cache.EXPECT().Get("code_email:alice@example.com").Return(value, nil)
			cache.EXPECT().CompareAndDelete("code_email:alice@example.com", value).DoAndReturn(
				func(string, string) (bool, error) {
					now = now.Add(delay)
					return true, nil
				})
			rec, _, err := ca.Authenticate([]byte("123456:email:alice@example.com"), "")
			if delay < 59*time.Second {
				if err != nil || rec == nil || rec.Lifetime != auth.Duration(time.Minute-delay) {
					t.Fatalf("incorrect remaining lifetime: %+v, %v", rec, err)
				}
			} else if rec != nil || !errors.Is(err, types.ErrExpired) {
				t.Fatalf("expired code produced an auth record: %+v, %v", rec, err)
			}
		})
	}
}

// TestCodeAuthenticationMatchesTokenPrecision checks fractional-second validity boundaries.
func TestCodeAuthenticationMatchesTokenPrecision(t *testing.T) {
	for _, tc := range []struct {
		name               string
		fraction, lifetime time.Duration
		wantLifetime       time.Duration
	}{
		{name: "half a second", fraction: 750 * time.Millisecond, lifetime: 500 * time.Millisecond},
		{name: "whole second lost to truncation", fraction: 750 * time.Millisecond, lifetime: 1100 * time.Millisecond},
		{name: "exact minimum", lifetime: time.Second},
		{name: "fraction below next whole second", lifetime: 1999 * time.Millisecond},
		{name: "usable fractional window", fraction: 750 * time.Millisecond, lifetime: 1250 * time.Millisecond, wantLifetime: 1250 * time.Millisecond},
		{name: "two whole seconds", lifetime: 2 * time.Second, wantLifetime: 2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ca, cache := setupCodeTest(t)
			now := time.Unix(1700000000, 0).Add(tc.fraction)
			ca.now = func() time.Time { return now }
			expiry := now.Add(tc.lifetime)
			cache.values["code_email:alice@example.com"] = "123456:0:" + types.Uid(123).String() + ":" + strconv.FormatInt(expiry.UnixNano(), 10)
			rec, _, err := ca.Authenticate([]byte("123456:email:alice@example.com"), "")
			if tc.wantLifetime == 0 {
				if rec != nil || !errors.Is(err, types.ErrExpired) {
					t.Fatalf("unusable token window accepted: %+v, %v", rec, err)
				}
			} else if err != nil || rec == nil || rec.Lifetime != auth.Duration(tc.wantLifetime) {
				t.Fatalf("incorrect usable lifetime: %+v, %v", rec, err)
			}
		})
	}
}

// TestCodeToTokenFlow checks redemption against the real token generator and validator.
func TestCodeToTokenFlow(t *testing.T) {
	tokenHandler := store.Store.GetAuthHandler("token")
	if !tokenHandler.IsInitialized() {
		if err := tokenHandler.Init([]byte("{\"key\":\"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\",\"serial_num\":1,\"expire_in\":3600}"), "token"); err != nil {
			t.Fatal(err)
		}
	}
	for _, lifetime := range []time.Duration{500 * time.Millisecond, time.Minute} {
		t.Run(lifetime.String(), func(t *testing.T) {
			ca, cache := setupCodeTest(t)
			// A scheduling delay must not make the near-expiry regression pass
			// merely because the code expired before redemption began.
			now := time.Now()
			ca.now = func() time.Time { return now }
			expiry := now.Add(lifetime)
			cache.values["code_email:alice@example.com"] = "123456:0:" + types.Uid(123).String() + ":" + strconv.FormatInt(expiry.UnixNano(), 10)
			rec, _, err := ca.Authenticate([]byte("123456:email:alice@example.com"), "")
			if lifetime < time.Second {
				if rec != nil || !errors.Is(err, types.ErrExpired) {
					t.Fatalf("near-expiry code reported success: %+v, %v", rec, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			secret, _, err := tokenHandler.GenSecret(rec)
			if err != nil {
				t.Fatal(err)
			}
			redeemed, _, err := tokenHandler.Authenticate(secret, "")
			if err != nil {
				t.Fatalf("successful code redemption produced unusable token: %v", err)
			}
			if redeemed.Uid != rec.Uid || redeemed.Features&auth.FeatureNoLogin == 0 {
				t.Fatalf("incorrect reset token: %+v", redeemed)
			}
		})
	}
}

// TestCodeIssuanceReclaimsRejectedRecords checks replacement independently of cache age.
func TestCodeIssuanceReclaimsRejectedRecords(t *testing.T) {
	for _, tc := range []struct {
		name, suffix string
		valid        bool
	}{
		{name: "expired regardless of cache age", suffix: ":expired"},
		{name: "legacy without deadline"},
		{name: "malformed deadline", suffix: ":invalid"},
		{name: "valid code", suffix: ":future", valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ca, cache := setupCodeTest(t)
			now := time.Now().UTC().Round(time.Millisecond)
			ca.now = func() time.Time { return now }
			suffix := tc.suffix
			switch suffix {
			case ":expired":
				suffix = ":" + strconv.FormatInt(now.Add(-time.Second).UnixNano(), 10)
			case ":future":
				suffix = ":" + strconv.FormatInt(now.Add(time.Hour).UnixNano(), 10)
			}
			key := "code_email:alice@example.com"
			original := "123456:0:" + types.Uid(123).String() + suffix
			cache.values[key] = original
			code, expiry, err := ca.GenSecret(&auth.Rec{Uid: types.Uid(123), Credential: "email:alice@example.com"})
			if tc.valid {
				if !errors.Is(err, types.ErrDuplicate) || cache.values[key] != original {
					t.Fatalf("valid code overwritten: %q, %v", cache.values[key], err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(cache.values[key], ":")
			if len(parts) != 4 || parts[0] != string(code) || parts[3] != strconv.FormatInt(expiry.UnixNano(), 10) {
				t.Fatalf("rejected record was not replaced: %v", parts)
			}
			if _, _, err := ca.Authenticate([]byte(string(code)+":email:alice@example.com"), ""); err != nil {
				t.Fatalf("replacement code unusable: %v", err)
			}
		})
	}
}

// TestCodeConcurrentIssuancePreservesWinner checks that exactly one replacement survives.
func TestCodeConcurrentIssuancePreservesWinner(t *testing.T) {
	ca, cache := setupCodeTest(t)
	now := time.Now().UTC().Round(time.Millisecond)
	ca.now = func() time.Time { return now }
	key := "code_email:alice@example.com"
	cache.values[key] = "123456:0:" + types.Uid(123).String() + ":" + strconv.FormatInt(now.Add(-time.Second).UnixNano(), 10)
	type result struct {
		code []byte
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 16)
	for range cap(results) {
		go func() {
			<-start
			code, _, err := ca.GenSecret(&auth.Rec{Uid: types.Uid(123), Credential: "email:alice@example.com"})
			results <- result{code, err}
		}()
	}
	close(start)
	var winner []byte
	for range cap(results) {
		result := <-results
		if result.err == nil {
			if winner != nil {
				t.Fatal("multiple replacement codes issued")
			}
			winner = result.code
		} else if !errors.Is(result.err, types.ErrDuplicate) {
			t.Fatal(result.err)
		}
	}
	if winner == nil || !strings.HasPrefix(cache.values[key], string(winner)+":") {
		t.Fatalf("winning code overwritten: %q", cache.values[key])
	}
}

// TestCodeCacheConflictsFailClosed checks stale writes, deletion failures, and consumed records.
func TestCodeCacheConflictsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name               string
		wrongCode, deleted bool
		cacheErr, wantErr  error
	}{
		{name: "late retry", wrongCode: true, wantErr: types.ErrFailed},
		{name: "already replaced or consumed", wantErr: types.ErrFailed},
		{name: "deletion failed", cacheErr: types.ErrInternal, wantErr: types.ErrInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ca, _ := setupCodeTest(t)
			cache := mock_store.NewMockPersistentCacheInterface(gomock.NewController(t))
			store.PCache = cache
			key := "code_email:alice@example.com"
			value := "123456:0:" + types.Uid(123).String() + ":" + strconv.FormatInt(time.Now().Add(time.Hour).UnixNano(), 10)
			cache.EXPECT().Get(key).Return(value, nil)
			code := "123456"
			if tc.wrongCode {
				code = "654321"
				cache.EXPECT().CompareAndSwap(key, value, strings.Replace(value, ":0:", ":1:", 1)).Return(false, nil)
			} else {
				cache.EXPECT().CompareAndDelete(key, value).Return(tc.deleted, tc.cacheErr)
			}
			rec, _, err := ca.Authenticate([]byte(code+":email:alice@example.com"), "")
			if rec != nil || !errors.Is(err, tc.wantErr) {
				t.Fatalf("cache conflict accepted: %+v, %v", rec, err)
			}
		})
	}
}

// TestCodeIssuanceLosesReplacementRace checks a concurrent replacement between read and swap.
func TestCodeIssuanceLosesReplacementRace(t *testing.T) {
	ca, _ := setupCodeTest(t)
	cache := mock_store.NewMockPersistentCacheInterface(gomock.NewController(t))
	store.PCache = cache
	key := "code_email:alice@example.com"
	cache.EXPECT().Expire(gomock.Any(), gomock.Any()).Return(nil)
	cache.EXPECT().Upsert(key, gomock.Any(), true).Return(types.ErrDuplicate)
	cache.EXPECT().Get(key).Return("123456:0:"+types.Uid(123).String(), nil)
	cache.EXPECT().CompareAndSwap(key, "123456:0:"+types.Uid(123).String(), gomock.Any()).Return(false, nil)
	if _, _, err := ca.GenSecret(&auth.Rec{Uid: types.Uid(123), Credential: "email:alice@example.com"}); !errors.Is(err, types.ErrDuplicate) {
		t.Fatalf("lost replacement race reported success: %v", err)
	}
}
