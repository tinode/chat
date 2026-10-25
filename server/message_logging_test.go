package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/tinode/chat/pbx"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store/types"
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
	} {
		t.Run(payload, func(t *testing.T) {
			output := captureMessageLogs(t)
			sess := test_makeSession(types.ZeroUid)
			sess.ver = 0 // Stop before account/storage operations.
			sess.dispatchRaw([]byte(payload))
			assertSecretNotLogged(t, output.String(), secret)
			if !strings.Contains(output.String(), "in: bytes=") {
				t.Fatal("message metadata missing")
			}
			if len(sess.send) == 0 {
				t.Fatal("message was not dispatched")
			}
		})
	}
}

func TestGrpcMessageLoggingOmitsSecrets(t *testing.T) {
	secret := "private-grpc-password-and-token-canary"
	for _, msg := range []*pbx.ClientMsg{
		{Message: &pbx.ClientMsg_Login{Login: &pbx.ClientLogin{Scheme: "basic", Secret: []byte(secret)}}},
		{Message: &pbx.ClientMsg_Login{Login: &pbx.ClientLogin{Scheme: "token", Secret: []byte(secret)}}},
		{Message: &pbx.ClientMsg_Acc{Acc: &pbx.ClientAcc{UserId: "new", Scheme: "basic", Secret: []byte(secret), TmpSecret: []byte(secret)}}},
		{Message: &pbx.ClientMsg_Pub{Pub: &pbx.ClientPub{Topic: "grpTest", Content: []byte(`"` + secret + `"`)}}},
	} {
		output := captureMessageLogs(t)
		sess := test_makeSession(types.ZeroUid)
		sess.ver = 0
		sess.dispatchGrpc(msg)
		assertSecretNotLogged(t, output.String(), secret)
		if !strings.Contains(output.String(), "grpc in: bytes=") || len(sess.send) == 0 {
			t.Fatal("message was not logged and dispatched")
		}
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
