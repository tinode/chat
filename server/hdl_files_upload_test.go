package main

import (
	"context"
	"encoding/base64"
	"errors"
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

type sizeTestUploadStream struct {
	grpc.ServerStream
	requests []*pbx.FileUpReq
	response *pbx.FileUpResp
	recvErr  error
}

func (s *sizeTestUploadStream) Context() context.Context { return context.Background() }
func (s *sizeTestUploadStream) Recv() (*pbx.FileUpReq, error) {
	if len(s.requests) == 0 {
		if s.recvErr != nil {
			return nil, s.recvErr
		}
		return nil, io.EOF
	}
	req := s.requests[0]
	s.requests = s.requests[1:]
	return req, nil
}
func (s *sizeTestUploadStream) SendAndClose(resp *pbx.FileUpResp) error {
	s.response = resp
	return nil
}

func TestGrpcUploadReaderSizeLimits(t *testing.T) {
	for _, tc := range []struct {
		name, first string
		chunks      []string
		limit       int64
		oversized   bool
		want        string
	}{
		{name: "initial content", first: "abcd", limit: 4, want: "abcd"},
		{name: "exact limit", first: "ab", chunks: []string{"", "cd"}, limit: 4, want: "abcd"},
		{name: "too large initial content", first: "abcde", limit: 4, oversized: true},
		{name: "too large across chunks", first: "ab", chunks: []string{"cd", "e"}, limit: 4, oversized: true, want: "abcd"},
		{name: "too large later chunk", chunks: []string{"abc", "de"}, limit: 4, oversized: true, want: "abc"},
		{name: "unlimited", first: "abc", chunks: []string{"defgh"}, want: "abcdefgh"},
		{name: "empty", limit: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &sizeTestUploadStream{}
			for _, chunk := range tc.chunks {
				stream.requests = append(stream.requests, &pbx.FileUpReq{Content: []byte(chunk)})
			}
			reader := &grpcUploadReader{stream: stream, content: []byte(tc.first), maxSize: tc.limit}
			// Small reads also exercise partially consumed chunks.
			var got []byte
			buf := make([]byte, 1)
			var err error
			for err == nil {
				var n int
				n, err = reader.Read(buf)
				got = append(got, buf[:n]...)
			}
			var sizeErr *http.MaxBytesError
			if tc.oversized {
				if !errors.As(err, &sizeErr) || sizeErr.Limit != tc.limit {
					t.Fatalf("expected size error, got %v", err)
				}
			} else if !errors.Is(err, io.EOF) {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if tc.limit > 0 && int64(len(got)) > tc.limit {
				t.Fatal("reader exceeded limit")
			}
		})
	}
}

func TestGrpcUploadReaderPropagatesReceiveError(t *testing.T) {
	want := errors.New("disconnected")
	reader := &grpcUploadReader{stream: &sizeTestUploadStream{recvErr: want}}
	if _, err := io.ReadAll(reader); !errors.Is(err, want) {
		t.Fatalf("lost receive error: %v", err)
	}
}

type sizeTestMedia struct {
	media.Handler
	content []byte
}

func (m *sizeTestMedia) Headers(string, *url.URL, http.Header, bool) (http.Header, int, error) {
	return nil, 0, nil
}
func (m *sizeTestMedia) Upload(_ *types.FileDef, reader io.Reader) (string, int64, error) {
	data, err := io.ReadAll(reader)
	m.content = data
	return "/file", int64(len(data)), err
}

func TestGrpcUploadEnforcesConfiguredTotalSize(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact limit", true: "oversized"}[oversized], func(t *testing.T) {
			ctrl := gomock.NewController(t)
			oldStore, oldFiles, oldLimit := store.Store, store.Files, globals.maxFileUploadSize
			t.Cleanup(func() { store.Store, store.Files, globals.maxFileUploadSize = oldStore, oldFiles, oldLimit })
			storage := mock_store.NewMockPersistentStorageInterface(ctrl)
			files := mock_store.NewMockFilePersistenceInterface(ctrl)
			handler := mock_auth.NewMockAuthHandler(ctrl)
			media := &sizeTestMedia{}
			store.Store, store.Files, globals.maxFileUploadSize = storage, files, 4
			storage.EXPECT().GetMediaHandler().Return(media)
			storage.EXPECT().GetLogicalAuthHandler("token").Return(handler)
			handler.EXPECT().Authenticate([]byte("secret"), "").Return(&auth.Rec{Uid: types.Uid(123), AuthLevel: auth.LevelAuth, Features: auth.FeatureValidated, State: types.StateOK}, nil, nil)
			storage.EXPECT().GetUidString().Return(types.Uid(456).String())
			if oversized {
				files.EXPECT().FinishUpload(gomock.Any(), false, int64(0)).Return(nil, nil)
			}
			stream := &sizeTestUploadStream{requests: []*pbx.FileUpReq{
				{Id: "upload", Auth: &pbx.Auth{Scheme: "token", Secret: base64.StdEncoding.EncodeToString([]byte("secret"))}, Content: []byte("ab")},
				{Content: []byte("cd")},
			}}
			if oversized {
				stream.requests = append(stream.requests, &pbx.FileUpReq{Content: []byte("e")})
			}
			if err := (&grpcNodeServer{}).LargeFileReceive(stream); err != nil {
				t.Fatal(err)
			}
			wantCode := int32(200)
			if oversized {
				wantCode = 413
			}
			if stream.response == nil || stream.response.Code != wantCode || stream.response.Id != "upload" {
				t.Fatalf("unexpected response: %v", stream.response)
			}
			if string(media.content) != "abcd" {
				t.Fatalf("wrong stored content: %q", media.content)
			}
		})
	}
}
