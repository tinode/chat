package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/auth/mock_auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
)

// TestOnLoginRejectsUnusableResetTokens checks the final token window and issuance failures.
func TestOnLoginRejectsUnusableResetTokens(t *testing.T) {
	for _, tc := range []struct {
		name          string
		expiry        time.Time
		features      auth.Feature
		generationErr error
		wantCode      int
	}{
		{name: "near expiry", expiry: time.Now().Add(500 * time.Millisecond), features: auth.FeatureNoLogin, wantCode: http.StatusUnauthorized},
		{name: "insufficient whole-second window", expiry: time.Now().Truncate(time.Second).Add(time.Second), features: auth.FeatureNoLogin, wantCode: http.StatusUnauthorized},
		{name: "usable reset token", expiry: time.Now().Add(time.Minute), features: auth.FeatureNoLogin, wantCode: http.StatusOK},
		{name: "issuance expired", generationErr: types.ErrExpired, features: auth.FeatureNoLogin, wantCode: http.StatusUnauthorized},
		{name: "issuance failed before session authentication", generationErr: types.ErrInternal, wantCode: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldStore := store.Store
			t.Cleanup(func() { store.Store = oldStore })
			ctrl := gomock.NewController(t)
			storage := mock_store.NewMockPersistentStorageInterface(ctrl)
			tokenHandler := mock_auth.NewMockAuthHandler(ctrl)
			store.Store = storage
			storage.EXPECT().GetLogicalAuthHandler("token").Return(tokenHandler)
			tokenHandler.EXPECT().GenSecret(gomock.Any()).Return([]byte("token"), tc.expiry, tc.generationErr)
			session := &Session{}
			reply := session.onLogin("login", types.TimeNow(), &auth.Rec{
				Uid: types.Uid(123), AuthLevel: auth.LevelAuth,
				Features: tc.features, Lifetime: auth.Duration(time.Minute),
			}, nil)
			if reply.Ctrl.Code != tc.wantCode {
				t.Fatalf("got %d, want %d", reply.Ctrl.Code, tc.wantCode)
			}
			if !session.uid.IsZero() {
				t.Fatal("failed or reset-only login authenticated the session")
			}
			if tc.wantCode != http.StatusOK && reply.Ctrl.Params != nil {
				t.Fatalf("failed login returned token parameters: %v", reply.Ctrl.Params)
			}
		})
	}
}
