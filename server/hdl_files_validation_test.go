package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/pbx"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/auth/mock_auth"
	"github.com/tinode/chat/server/media"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
	"google.golang.org/grpc"
)

type validationDownloadStream struct {
	grpc.ServerStream
	response *pbx.FileDownResp
}

func (s *validationDownloadStream) Context() context.Context { return context.Background() }
func (s *validationDownloadStream) Send(resp *pbx.FileDownResp) error {
	s.response = resp
	return nil
}

type validationUploadStream struct {
	grpc.ServerStream
	request  *pbx.FileUpReq
	response *pbx.FileUpResp
}

func (s *validationUploadStream) Context() context.Context { return context.Background() }
func (s *validationUploadStream) Recv() (*pbx.FileUpReq, error) {
	if s.request == nil {
		return nil, io.EOF
	}
	req := s.request
	s.request = nil
	return req, nil
}
func (s *validationUploadStream) SendAndClose(resp *pbx.FileUpResp) error {
	s.response = resp
	return nil
}

func TestGrpcFilesWithoutAuthenticationDoNotPanic(t *testing.T) {
	oldStore, oldSessions := store.Store, globals.sessionStore
	t.Cleanup(func() { store.Store, globals.sessionStore = oldStore, oldSessions })
	globals.sessionStore = &SessionStore{sessCache: make(map[string]*Session)}
	storage := mock_store.NewMockPersistentStorageInterface(gomock.NewController(t))
	storage.EXPECT().GetMediaHandler().Return(nil).AnyTimes()
	store.Store = storage

	for _, tc := range []struct {
		name string
		auth *pbx.Auth
	}{
		{"missing", nil},
		{"empty", &pbx.Auth{}},
		// An empty scheme ignores the secret and takes the session fallback.
		{"missing scheme", &pbx.Auth{Secret: "not-base64!"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			down := &validationDownloadStream{}
			if err := (&grpcNodeServer{}).LargeFileServe(&pbx.FileDownReq{Id: "download", Auth: tc.auth}, down); err != nil {
				t.Fatal(err)
			}
			if down.response == nil || down.response.Code != 401 || down.response.Id != "download" {
				t.Fatalf("unexpected download response: %v", down.response)
			}
			up := &validationUploadStream{request: &pbx.FileUpReq{Id: "upload", Auth: tc.auth}}
			if err := (&grpcNodeServer{}).LargeFileReceive(up); err != nil {
				t.Fatal(err)
			}
			if up.response == nil || up.response.Code != 401 || up.response.Id != "upload" {
				t.Fatalf("unexpected upload response: %v", up.response)
			}
		})
	}
}

type validationRedirectMedia struct {
	media.Handler
}

func (*validationRedirectMedia) Headers(string, *url.URL, http.Header, bool) (http.Header, int, error) {
	return http.Header{"Location": {"/file"}}, http.StatusTemporaryRedirect, nil
}

func TestGrpcFilesDelegateEmptySecretToAuthenticator(t *testing.T) {
	ctrl := gomock.NewController(t)
	oldStore := store.Store
	t.Cleanup(func() { store.Store = oldStore })
	storage := mock_store.NewMockPersistentStorageInterface(ctrl)
	handler := mock_auth.NewMockAuthHandler(ctrl)
	store.Store = storage
	storage.EXPECT().GetLogicalAuthHandler("custom").Return(handler).Times(2)
	// Whether an empty secret is acceptable is the authenticator's decision.
	handler.EXPECT().Authenticate(gomock.Len(0), "").Return(&auth.Rec{
		Uid: types.Uid(123), AuthLevel: auth.LevelAuth,
		Features: auth.FeatureValidated, State: types.StateOK,
	}, nil, nil).Times(2)
	storage.EXPECT().GetMediaHandler().Return(&validationRedirectMedia{}).Times(2)

	credentials := &pbx.Auth{Scheme: "custom"}
	down := &validationDownloadStream{}
	if err := (&grpcNodeServer{}).LargeFileServe(&pbx.FileDownReq{Id: "download", Auth: credentials}, down); err != nil {
		t.Fatal(err)
	}
	if down.response == nil || down.response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("empty secret was not delegated for download: %v", down.response)
	}
	up := &validationUploadStream{request: &pbx.FileUpReq{Id: "upload", Auth: credentials}}
	if err := (&grpcNodeServer{}).LargeFileReceive(up); err != nil {
		t.Fatal(err)
	}
	if up.response == nil || up.response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("empty secret was not delegated for upload: %v", up.response)
	}
}

func TestFileAuthenticationWithoutSchemeUsesSession(t *testing.T) {
	oldSessions, oldUsers := globals.sessionStore, store.Users
	t.Cleanup(func() { globals.sessionStore, store.Users = oldSessions, oldUsers })
	uid := types.Uid(123)
	users := mock_store.NewMockUsersPersistenceInterface(gomock.NewController(t))
	users.EXPECT().Get(uid).Return(&types.User{State: types.StateOK}, nil).AnyTimes()
	store.Users = users
	globals.sessionStore = &SessionStore{sessCache: map[string]*Session{
		"known-session": {uid: uid, authLvl: auth.LevelAuth, proto: WEBSOCK},
	}}
	got, challenge, err := authFileRequest("", "ignored-not-base64!", "known-session", "")
	if err != nil || challenge != nil || got != uid {
		t.Fatalf("valid session fallback rejected: uid=%v challenge=%q err=%v", got, challenge, err)
	}
}
