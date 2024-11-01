package apple

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthenticator_Authenticate(t *testing.T) {
	a := authenticator{}
	config := `
	{
		"apple_server_url": "http://127.0.0.1:8080/auth",
		"client_id": "test",
		"team_id": "test",
		"key_id": "test",
		"private_key_path": "private_key.p8",
	}`
	a.Init([]byte(config), "apple")

	if !a.IsInitialized() {
		t.Error("apple authenticator not initialized")
	}

	_, _, err := a.Authenticate([]byte("test"), "")
	require.NoError(t, err)
}
