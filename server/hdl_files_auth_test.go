package main

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/auth/mock_auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
)

func TestFileAuthenticationRestrictions(t *testing.T) {
	uid := types.Uid(123)
	for _, tc := range []struct {
		name     string
		level    auth.Level
		state    types.ObjState
		features auth.Feature
		required bool
		creds    []types.Credential
		credErr  error
		wantErr  error
	}{
		{name: "active", level: auth.LevelAuth, state: types.StateOK},
		{name: "anonymous account", level: auth.LevelAnon, state: types.StateOK},
		{name: "suspended", level: auth.LevelAuth, state: types.StateSuspended, wantErr: types.ErrPermissionDenied},
		{name: "deleted", level: auth.LevelAuth, state: types.StateDeleted, wantErr: types.ErrPermissionDenied},
		{name: "reset token", level: auth.LevelAuth, state: types.StateOK, features: auth.FeatureNoLogin, wantErr: types.ErrPermissionDenied},
		{name: "no auth level", state: types.StateOK, wantErr: types.ErrPermissionDenied},
		{name: "missing validation", level: auth.LevelAuth, state: types.StateOK, required: true, wantErr: types.ErrPermissionDenied},
		{name: "validated credential", level: auth.LevelAuth, state: types.StateOK, required: true, creds: []types.Credential{{Method: "email"}}},
		{name: "validated token", level: auth.LevelAuth, state: types.StateOK, required: true, features: auth.FeatureValidated},
		{name: "validation lookup fails", level: auth.LevelAuth, state: types.StateOK, required: true, credErr: types.ErrInternal, wantErr: types.ErrInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			oldStore, oldUsers, oldValidators := store.Store, store.Users, globals.authValidators
			t.Cleanup(func() { store.Store, store.Users, globals.authValidators = oldStore, oldUsers, oldValidators })
			storage := mock_store.NewMockPersistentStorageInterface(ctrl)
			users := mock_store.NewMockUsersPersistenceInterface(ctrl)
			handler := mock_auth.NewMockAuthHandler(ctrl)
			store.Store, store.Users = storage, users
			globals.authValidators = nil
			if tc.required {
				globals.authValidators = map[auth.Level][]string{auth.LevelAuth: {"email"}}
			}
			storage.EXPECT().GetLogicalAuthHandler("token").Return(handler)
			handler.EXPECT().Authenticate([]byte("secret"), "remote").Return(&auth.Rec{Uid: uid, AuthLevel: tc.level, State: tc.state, Features: tc.features}, nil, nil)
			if tc.required && tc.features&auth.FeatureValidated == 0 {
				users.EXPECT().GetAllCreds(uid, "", true).Return(tc.creds, tc.credErr)
			}
			got, challenge, err := authFileRequest("token", base64.StdEncoding.EncodeToString([]byte("secret")), "", "remote")
			if !errors.Is(err, tc.wantErr) || challenge != nil {
				t.Fatalf("got challenge=%q err=%v, want %v", challenge, err, tc.wantErr)
			}
			if (tc.wantErr == nil && got != uid) || (tc.wantErr != nil && !got.IsZero()) {
				t.Fatalf("unexpected UID %v", got)
			}
		})
	}
}

func TestFileAuthenticationChecksStoredAccountState(t *testing.T) {
	for _, state := range []types.ObjState{types.StateOK, types.StateSuspended, types.StateDeleted} {
		for _, session := range []bool{false, true} {
			t.Run(state.String()+"/session="+map[bool]string{false: "false", true: "true"}[session], func(t *testing.T) {
				ctrl := gomock.NewController(t)
				oldStore, oldUsers, oldSessions, oldValidators := store.Store, store.Users, globals.sessionStore, globals.authValidators
				t.Cleanup(func() {
					store.Store, store.Users, globals.sessionStore, globals.authValidators = oldStore, oldUsers, oldSessions, oldValidators
				})
				storage := mock_store.NewMockPersistentStorageInterface(ctrl)
				users := mock_store.NewMockUsersPersistenceInterface(ctrl)
				store.Store, store.Users = storage, users
				globals.authValidators = nil
				uid := types.Uid(123)
				user := &types.User{State: state}
				users.EXPECT().Get(uid).Return(user, nil)
				method, secret, sid := "token", base64.StdEncoding.EncodeToString([]byte("secret")), ""
				if session {
					method, secret, sid = "", "", "sid"
					globals.sessionStore = &SessionStore{sessCache: map[string]*Session{"sid": {uid: uid, authLvl: auth.LevelAuth, proto: WEBSOCK}}}
				} else {
					handler := mock_auth.NewMockAuthHandler(ctrl)
					storage.EXPECT().GetLogicalAuthHandler("token").Return(handler)
					handler.EXPECT().Authenticate([]byte("secret"), "remote").Return(&auth.Rec{Uid: uid, AuthLevel: auth.LevelAuth, State: types.StateUndefined}, nil, nil)
				}
				got, _, err := authFileRequest(method, secret, sid, "remote")
				if state == types.StateOK {
					if err != nil || got != uid {
						t.Fatalf("active account rejected: %v", err)
					}
				} else if !errors.Is(err, types.ErrPermissionDenied) || !got.IsZero() {
					t.Fatalf("disabled account accepted: %v %v", got, err)
				}
			})
		}
	}
}

func TestFileAuthenticationPreservesSignupAndChallenges(t *testing.T) {
	oldStore, oldSessions := store.Store, globals.sessionStore
	t.Cleanup(func() { store.Store, globals.sessionStore = oldStore, oldSessions })
	globals.sessionStore = &SessionStore{sessCache: make(map[string]*Session)}
	if uid, _, err := authFileRequest("", "", "", ""); !uid.IsZero() || err != nil {
		t.Fatalf("signup rejected: %v", err)
	}
	ctrl := gomock.NewController(t)
	storage := mock_store.NewMockPersistentStorageInterface(ctrl)
	handler := mock_auth.NewMockAuthHandler(ctrl)
	store.Store = storage
	storage.EXPECT().GetLogicalAuthHandler("rest").Return(handler)
	handler.EXPECT().Authenticate([]byte("secret"), "remote").Return(nil, []byte("challenge"), nil)
	uid, challenge, err := authFileRequest("rest", base64.StdEncoding.EncodeToString([]byte("secret")), "", "remote")
	if !uid.IsZero() || string(challenge) != "challenge" || err != nil {
		t.Fatalf("unexpected challenge response: %v %q %v", uid, challenge, err)
	}
}
