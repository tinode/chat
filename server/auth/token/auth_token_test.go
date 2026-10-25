package token

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store/types"
)

// TestTokenAbsoluteDeadline verifies that storage delays cannot move expiration forward.
func TestTokenAbsoluteDeadline(t *testing.T) {
	start := time.Unix(1700000000, 0).Add(250 * time.Millisecond)
	deadline := start.Add(2750 * time.Millisecond)
	ta := &authenticator{hmacSalt: make([]byte, 32), lifetime: time.Hour}
	for _, tc := range []struct {
		name    string
		delay   time.Duration
		expired bool
	}{
		{"immediate", 0, false},
		{"lookup within validity", 500 * time.Millisecond, false},
		{"minimum window exhausted", 2 * time.Second, true},
		{"lookup beyond deadline", 4 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &auth.Rec{Uid: types.Uid(123), Lifetime: auth.Duration(2750 * time.Millisecond), ExpiresAt: deadline, Features: auth.FeatureNoLogin}
			secret, expiry, err := ta.genSecretAt(rec, start.Add(tc.delay))
			if tc.expired {
				if !errors.Is(err, types.ErrExpired) || len(secret) != 0 {
					t.Fatalf("expired deadline issued token: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded := time.Unix(int64(binary.LittleEndian.Uint32(secret[8:12])), 0)
			if !expiry.Equal(encoded) || !expiry.Equal(deadline.Truncate(time.Second)) {
				t.Fatalf("deadline shifted: encoded=%v returned=%v deadline=%v", encoded, expiry, deadline)
			}
		})
	}
}

// TestTokenExpirationPrecision checks issuance against the validator's whole-second window.
func TestTokenExpirationPrecision(t *testing.T) {
	ta := &authenticator{hmacSalt: make([]byte, 32), lifetime: time.Hour}
	now := time.Unix(1700000000, 0).Add(750 * time.Millisecond)
	for _, lifetime := range []time.Duration{500 * time.Millisecond, 1100 * time.Millisecond, 1250 * time.Millisecond, time.Minute} {
		t.Run(lifetime.String(), func(t *testing.T) {
			deadline := now.Add(lifetime)
			secret, expiry, err := ta.genSecretAt(&auth.Rec{ExpiresAt: deadline}, now)
			if !deadline.Truncate(time.Second).After(now.Add(time.Second)) {
				if !errors.Is(err, types.ErrExpired) || len(secret) != 0 {
					t.Fatalf("unusable token issued: %v", err)
				}
			} else if err != nil || expiry.After(deadline) {
				t.Fatalf("valid deadline mishandled: %v, %v", expiry, err)
			}
		})
	}
}
