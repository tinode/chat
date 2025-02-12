package code

import (
    "testing"
    "encoding/json"
    "github.com/tinode/chat/server/store/types"
    "time"
)


// Test generated using Keploy
func TestInit_BlankName_Error(t *testing.T) {
    var ca authenticator
    err := ca.Init(json.RawMessage(`{"code_length":6,"expire_in":300,"max_retries":3}`), "")
    if err == nil || err.Error() != "auth_code: authenticator name cannot be blank" {
        t.Errorf("Expected error for blank name, got: %v", err)
    }
}

// Test generated using Keploy
func TestInit_InvalidCodeLength_Error(t *testing.T) {
    var ca authenticator
    err := ca.Init(json.RawMessage(`{"code_length":3,"expire_in":300,"max_retries":3}`), "test_auth")
    if err == nil || err.Error() != "auth_code: invalid code length" {
        t.Errorf("Expected error for invalid code length, got: %v", err)
    }
}


// Test generated using Keploy
func TestAuthenticate_MalformedSecret_Error(t *testing.T) {
    ca := authenticator{name: "test_auth"}
    _, _, err := ca.Authenticate([]byte("malformed_secret"), "127.0.0.1")
    if err == nil || err != types.ErrMalformed {
        t.Errorf("Expected ErrMalformed for malformed secret, got: %v", err)
    }
}


// Test generated using Keploy
func TestSanitizeKey_PercentReplacement(t *testing.T) {
    input := "key%with%percent"
    expected := "key/with/percent"
    result := sanitizeKey(input)
    if result != expected {
        t.Errorf("Expected sanitized key to be %q, got: %q", expected, result)
    }
}


// Test generated using Keploy
func TestInit_ValidParameters_Success(t *testing.T) {
    var ca authenticator
    err := ca.Init(json.RawMessage(`{"code_length":6,"expire_in":300,"max_retries":3}`), "test_auth")
    if err != nil {
        t.Errorf("Expected no error for valid parameters, got: %v", err)
    }
    if ca.name != "test_auth" {
        t.Errorf("Expected authenticator name to be 'test_auth', got: %s", ca.name)
    }
    if ca.codeLength != 6 {
        t.Errorf("Expected code length to be 6, got: %d", ca.codeLength)
    }
    if ca.lifetime != 300*time.Second {
        t.Errorf("Expected lifetime to be 300 seconds, got: %v", ca.lifetime)
    }
    if ca.maxRetries != 3 {
        t.Errorf("Expected max retries to be 3, got: %d", ca.maxRetries)
    }
}

