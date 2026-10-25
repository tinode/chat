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
		{name: "provider extends deadline", expiry: time.Now().Add(time.Hour), features: auth.FeatureNoLogin, wantCode: http.StatusUnauthorized},
		{name: "provider omits expiration", features: auth.FeatureNoLogin, wantCode: http.StatusUnauthorized},
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
			deadline := time.Now().Add(time.Minute)
			tokenHandler.EXPECT().GenSecret(gomock.Any()).DoAndReturn(func(rec *auth.Rec) ([]byte, time.Time, error) {
				if tc.features&auth.FeatureNoLogin != 0 && !rec.ExpiresAt.Equal(deadline) {
					t.Fatal("login lost the absolute reset deadline")
				}
				if tc.features&auth.FeatureNoLogin == 0 && !rec.ExpiresAt.IsZero() {
					t.Fatal("regular login did not renew token validity")
				}
				return []byte("token"), tc.expiry, tc.generationErr
			})
			session := &Session{}
			reply := session.onLogin("login", types.TimeNow(), &auth.Rec{
				Uid: types.Uid(123), AuthLevel: auth.LevelAuth,
				Features: tc.features, Lifetime: auth.Duration(time.Minute), ExpiresAt: deadline,
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

// TestCreateUserLoginFailure verifies that token issuance errors cannot panic account creation.
func TestCreateUserLoginFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"internal", types.ErrInternal, http.StatusInternalServerError},
		{"credentials", types.ErrCredentials, http.StatusMultipleChoices},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldStore, oldUsers := store.Store, store.Users
			oldValidators := globals.authValidators
			t.Cleanup(func() { store.Store, store.Users = oldStore, oldUsers; globals.authValidators = oldValidators })
			globals.authValidators = nil
			ctrl := gomock.NewController(t)
			storage := mock_store.NewMockPersistentStorageInterface(ctrl)
			users := mock_store.NewMockUsersPersistenceInterface(ctrl)
			handler := mock_auth.NewMockAuthHandler(ctrl)
			store.Store, store.Users = storage, users
			uid := types.Uid(123)
			storage.EXPECT().GetLogicalAuthHandler("basic").Return(handler)
			handler.EXPECT().IsUnique(gomock.Any(), gomock.Any()).Return(true, nil)
			users.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(user *types.User, private any) (*types.User, error) { user.SetUid(uid); return user, nil })
			handler.EXPECT().AddRecord(gomock.Any(), gomock.Any(), gomock.Any()).Return(&auth.Rec{Uid: uid, AuthLevel: auth.LevelAuth}, nil)
			storage.EXPECT().GetLogicalAuthHandler("token").Return(handler).Times(2)
			gomock.InOrder(
				handler.EXPECT().GenSecret(gomock.Any()).Return([]byte("credential-token"), time.Now().Add(time.Hour), nil),
				handler.EXPECT().GenSecret(gomock.Any()).Return(nil, time.Time{}, tc.err),
			)
			session := &Session{send: make(chan any, 2)}
			replyCreateUser(session, &ClientComMessage{Id: "create", Timestamp: types.TimeNow(), Acc: &MsgClientAcc{Login: true, Scheme: "basic"}}, nil)
			if len(session.send) != 1 {
				t.Fatalf("got %d replies", len(session.send))
			}
			reply := (<-session.send).(*ServerComMessage)
			if reply.Ctrl.Code != tc.status || reply.Ctrl.Params != nil {
				t.Fatalf("unexpected reply: %+v", reply.Ctrl)
			}
			if !session.uid.IsZero() {
				t.Fatal("failed login authenticated session")
			}

		})
	}
}
