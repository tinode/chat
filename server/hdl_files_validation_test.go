package main

import (
	"context"
	"io"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/pbx"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
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

func TestGrpcFilesRejectMissingAuthentication(t *testing.T) {
	oldStore := store.Store
	t.Cleanup(func() { store.Store = oldStore })
	storage := mock_store.NewMockPersistentStorageInterface(gomock.NewController(t))
	storage.EXPECT().GetMediaHandler().Return(nil).AnyTimes()
	store.Store = storage

	for _, tc := range []struct {
		name string
		auth *pbx.Auth
	}{
		{"missing", nil},
		{"empty", &pbx.Auth{}},
		{"missing secret", &pbx.Auth{Scheme: "token"}},
		{"missing scheme", &pbx.Auth{Secret: "secret"}},
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
