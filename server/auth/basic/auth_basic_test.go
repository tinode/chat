package basic

import (
    "testing"


)


// Test generated using Keploy
func TestCheckLoginPolicy_ShortUsername_Error(t *testing.T) {
    auth := authenticator{
        minLoginLength: 5,
    }
    err := auth.checkLoginPolicy("abc")
    if err == nil {
        t.Errorf("Expected error for short username, got nil")
    }
}

// Test generated using Keploy
func TestCheckPasswordPolicy_ShortPassword_Error(t *testing.T) {
    auth := authenticator{
        minPasswordLength: 6,
    }
    err := auth.checkPasswordPolicy("123")
    if err == nil {
        t.Errorf("Expected error for short password, got nil")
    }
}


// Test generated using Keploy
func TestParseSecret_ValidSecret_Success(t *testing.T) {
    secret := []byte("username:password")
    uname, password, err := parseSecret(secret)
    if err != nil {
        t.Errorf("Expected no error, got %v", err)
    }
    if uname != "username" {
        t.Errorf("Expected username 'username', got %v", uname)
    }
    if password != "password" {
        t.Errorf("Expected password 'password', got %v", password)
    }
}


// Test generated using Keploy
func TestParseSecret_MalformedSecret_Error(t *testing.T) {
    secret := []byte("invalidsecret")
    _, _, err := parseSecret(secret)
    if err == nil {
        t.Errorf("Expected error for malformed secret, got nil")
    }
}


// Test generated using Keploy
func TestIsInitialized_Initialized_ReturnsTrue(t *testing.T) {
    auth := authenticator{
        name: "test_authenticator",
    }
    if !auth.IsInitialized() {
        t.Errorf("Expected IsInitialized to return true, got false")
    }
}


// Test generated using Keploy
func TestCheckLoginPolicy_ValidUsername_NoError(t *testing.T) {
    auth := authenticator{
        minLoginLength: 5,
    }
    err := auth.checkLoginPolicy("validUser")
    if err != nil {
        t.Errorf("Expected no error for valid username, got %v", err)
    }
}


// Test generated using Keploy
func TestCheckPasswordPolicy_ValidPassword_NoError(t *testing.T) {
    auth := authenticator{
        minPasswordLength: 6,
    }
    err := auth.checkPasswordPolicy("securePass")
    if err != nil {
        t.Errorf("Expected no error for valid password, got %v", err)
    }
}

