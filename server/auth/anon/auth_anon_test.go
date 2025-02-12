package anon

import (
    "testing"
    "github.com/tinode/chat/server/auth"
    "github.com/tinode/chat/server/store/types"

)


// Test generated using Keploy
func TestInit_ValidName_Success(t *testing.T) {
    a := &authenticator{}
    err := a.Init(nil, "test_authenticator")
    if err != nil {
        t.Errorf("Expected no error, got %v", err)
    }
    if a.name != "test_authenticator" {
        t.Errorf("Expected name to be 'test_authenticator', got %v", a.name)
    }
}

// Test generated using Keploy
func TestInit_EmptyName_Error(t *testing.T) {
    a := &authenticator{}
    err := a.Init(nil, "")
    if err == nil || err.Error() != "auth_anonymous: authenticator name cannot be blank" {
        t.Errorf("Expected error 'auth_anonymous: authenticator name cannot be blank', got %v", err)
    }
}


// Test generated using Keploy
func TestGetRealName_ReturnsAnonymous(t *testing.T) {
    a := authenticator{}
    name := a.GetRealName()
    if name != "anonymous" {
        t.Errorf("Expected 'anonymous', got %v", name)
    }
}


// Test generated using Keploy
func TestAddRecord_AuthLevelNone_SetToLevelAnon(t *testing.T) {
    a := authenticator{}
    rec := &auth.Rec{AuthLevel: auth.LevelNone}
    updatedRec, err := a.AddRecord(rec, nil, "")
    if err != nil {
        t.Errorf("Expected no error, got %v", err)
    }
    if updatedRec.AuthLevel != auth.LevelAnon {
        t.Errorf("Expected AuthLevel to be LevelAnon, got %v", updatedRec.AuthLevel)
    }
}


// Test generated using Keploy
func TestAuthenticate_AlwaysUnsupported(t *testing.T) {
    a := authenticator{}
    rec, secret, err := a.Authenticate(nil, "")
    if rec != nil || secret != nil || err != types.ErrUnsupported {
        t.Errorf("Expected nil, nil, and ErrUnsupported, got %v, %v, %v", rec, secret, err)
    }
}


// Test generated using Keploy
func TestGenSecret_AlwaysUnsupported(t *testing.T) {
    a := authenticator{}
    secret, expiry, err := a.GenSecret(nil)
    if secret != nil || !expiry.IsZero() || err != types.ErrUnsupported {
        t.Errorf("Expected nil, zero time, and ErrUnsupported, got %v, %v, %v", secret, expiry, err)
    }
}

