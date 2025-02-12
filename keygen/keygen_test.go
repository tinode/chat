package main

import (
    "testing"
)


// Test generated using Keploy
func TestGenerate_ValidInputs_Success(t *testing.T) {
    sequence := 1
    isRoot := 0
    hmacSalt := ""

    exitCode := generate(sequence, isRoot, hmacSalt)

    if exitCode != 0 {
        t.Errorf("Expected exit code 0, got %d", exitCode)
    }
}

// Test generated using Keploy
func TestGenerate_InvalidHMACSalt_Error(t *testing.T) {
    sequence := 1
    isRoot := 0
    hmacSalt := "invalid_base64"

    exitCode := generate(sequence, isRoot, hmacSalt)

    if exitCode != 1 {
        t.Errorf("Expected exit code 1 for invalid HMAC salt, got %d", exitCode)
    }
}


// Test generated using Keploy
func TestValidate_InvalidAPIKey_Error(t *testing.T) {
    apiKey := "invalid_api_key"
    hmacSalt := "valid_base64_salt"

    exitCode := validate(apiKey, hmacSalt)

    if exitCode != 1 {
        t.Errorf("Expected exit code 1 for invalid API key, got %d", exitCode)
    }
}


// Test generated using Keploy
func TestGenerate_IsRootFlag_Success(t *testing.T) {
    sequence := 1
    isRoot := 1
    hmacSalt := ""

    exitCode := generate(sequence, isRoot, hmacSalt)

    if exitCode != 0 {
        t.Errorf("Expected exit code 0 for isRoot flag set to 1, got %d", exitCode)
    }
}


// Test generated using Keploy
func TestValidate_EmptyHMACSalt_Error(t *testing.T) {
    apiKey := "valid_api_key"
    hmacSalt := ""

    exitCode := validate(apiKey, hmacSalt)

    if exitCode != 1 {
        t.Errorf("Expected exit code 1 for empty HMAC salt, got %d", exitCode)
    }
}

