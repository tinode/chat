package main

import (
	"bytes"
	"container/list"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/tinode/chat/pbx"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/mock_store"
	"github.com/tinode/chat/server/store/types"
	"google.golang.org/grpc"
)

func captureMessageLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	oldInfo, oldWarn, oldErr := logs.Info.Writer(), logs.Warn.Writer(), logs.Err.Writer()
	logs.Info.SetOutput(&output)
	logs.Warn.SetOutput(&output)
	logs.Err.SetOutput(&output)
	t.Cleanup(func() { logs.Info.SetOutput(oldInfo); logs.Warn.SetOutput(oldWarn); logs.Err.SetOutput(oldErr) })
	return &output
}

func assertSecretNotLogged(t *testing.T, output, secret string) {
	t.Helper()
	for _, value := range []string{secret, base64.StdEncoding.EncodeToString([]byte(secret))} {
		if strings.Contains(output, value) {
			t.Fatalf("sensitive value appeared in logs: %q", output)
		}
	}
}

func TestRawMessageLoggingOmitsSecrets(t *testing.T) {
	secret := "private-password-and-token-canary"
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	for _, payload := range []string{
		fmt.Sprintf(`{"login":{"scheme":"basic","secret":%q}}`, encoded),
		fmt.Sprintf(`{"login":{"scheme":"token","secret":%q}}`, encoded),
		fmt.Sprintf(`{"acc":{"user":"new","scheme":"basic","secret":%q,"tmpscheme":"token","tmpsecret":%q}}`, encoded, encoded),
		fmt.Sprintf(`{"pub":{"topic":"grpTest","content":%q}}`, secret),
		fmt.Sprintf(`{"login":{"secret":%q}`, encoded), // malformed JSON
		fmt.Sprintf(`{"get":{"topic":"me","desc":{"ims":%q}}}`, secret),
		`{"pub":{"topic":"grpTest","content":987654321987654321e9999}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			output := captureMessageLogs(t)
			sess := test_makeSession(types.ZeroUid)
			sess.ver = 0 // Stop before account/storage operations.
			sess.dispatchRaw([]byte(payload))
			assertSecretNotLogged(t, output.String(), secret)
			assertSecretNotLogged(t, output.String(), "987654321987654321e9999")
			if !strings.Contains(output.String(), "in: bytes=") {
				t.Fatal("message metadata missing")
			}
			if len(sess.send) == 0 {
				t.Fatal("message was not dispatched")
			}
		})
	}
}

// loggingMessageStream exercises the production receive loop with one message.
type loggingMessageStream struct {
	grpc.ServerStream
	message *pbx.ClientMsg
	replies chan *pbx.ServerMsg
}

// Context returns the stream context.
func (*loggingMessageStream) Context() context.Context { return context.Background() }

// Recv supplies one message and waits for dispatch to produce a reply before closing.
func (s *loggingMessageStream) Recv() (*pbx.ClientMsg, error) {
	if s.message != nil {
		msg := s.message
		s.message = nil
		return msg, nil
	}
	select {
	case <-s.replies:
		return nil, io.EOF
	case <-time.After(5 * time.Second):
		return nil, context.DeadlineExceeded
	}
}

// Send records the reply produced by dispatch.
func (s *loggingMessageStream) Send(msg *pbx.ServerMsg) error { s.replies <- msg; return nil }

// TestGrpcMessageLoggingOmitsSecrets covers the receive loop and protobuf decoding.
func TestGrpcMessageLoggingOmitsSecrets(t *testing.T) {
	secret := "private-grpc-password-and-token-canary"
	for _, msg := range []*pbx.ClientMsg{
		{Message: &pbx.ClientMsg_Login{Login: &pbx.ClientLogin{Scheme: "basic", Secret: []byte(secret)}}},
		{Message: &pbx.ClientMsg_Login{Login: &pbx.ClientLogin{Scheme: "token", Secret: []byte(secret)}}},
		{Message: &pbx.ClientMsg_Acc{Acc: &pbx.ClientAcc{UserId: "new", Scheme: "basic", Secret: []byte(secret), TmpSecret: []byte(secret)}}},
		{Message: &pbx.ClientMsg_Pub{Pub: &pbx.ClientPub{Topic: "grpTest", Content: []byte(`"` + secret + `"`)}}},
		{Message: &pbx.ClientMsg_Pub{Pub: &pbx.ClientPub{Topic: "grpTest", Content: []byte(`{"private":"` + secret + `"`)}}},
		{Message: &pbx.ClientMsg_Pub{Pub: &pbx.ClientPub{Topic: "grpTest", Content: []byte(`987654321987654321e9999`)}}},
	} {
		t.Run(fmt.Sprintf("%T/%s", msg.Message, msg.GetPub().GetContent()), func(t *testing.T) {
			output := captureMessageLogs(t)
			oldStore, oldSessions := store.Store, globals.sessionStore
			t.Cleanup(func() { store.Store, globals.sessionStore = oldStore, oldSessions })
			storage := mock_store.NewMockPersistentStorageInterface(gomock.NewController(t))
			storage.EXPECT().GetUidString().Return("logging-session")
			store.Store = storage
			globals.sessionStore = &SessionStore{sessCache: make(map[string]*Session), lru: list.New(), lifeTime: time.Minute}
			stream := &loggingMessageStream{message: msg, replies: make(chan *pbx.ServerMsg, 1)}
			if err := (&grpcNodeServer{}).MessageLoop(stream); err != nil {
				t.Fatal(err)
			}
			assertSecretNotLogged(t, output.String(), secret)
			assertSecretNotLogged(t, output.String(), "987654321987654321e9999")
			if !strings.Contains(output.String(), "grpc in: bytes=") {
				t.Fatal("message metadata missing")
			}
		})
	}
}

func TestMalformedFileAuthenticationDoesNotLogSecret(t *testing.T) {
	output := captureMessageLogs(t)
	secret := "invalid-base64-authentication-canary!"
	if _, _, err := authFileRequest("token", secret, "", ""); err == nil {
		t.Fatal("malformed authentication accepted")
	}
	assertSecretNotLogged(t, output.String(), secret)
}
