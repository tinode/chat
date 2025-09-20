package store

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestNewEncryptionService(t *testing.T) {
	// Test with disabled encryption
	es, err := NewEncryptionService(false, nil)
	if err != nil {
		t.Fatalf("Failed to create disabled encryption service: %v", err)
	}
	if es.IsEnabled() {
		t.Error("Encryption service should be disabled")
	}

	// Test with valid key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	es, err = NewEncryptionService(true, key)
	if err != nil {
		t.Fatalf("Failed to create enabled encryption service: %v", err)
	}
	if !es.IsEnabled() {
		t.Error("Encryption service should be enabled")
	}

	// Test with invalid key length
	_, err = NewEncryptionService(true, []byte("short"))
	if err == nil {
		t.Error("Should fail with short key")
	}

	// Test with empty key when enabled
	_, err = NewEncryptionService(true, nil)
	if err == nil {
		t.Error("Should fail with empty key when enabled")
	}
}

func TestEncryptDecryptContent(t *testing.T) {
	// Create encryption service
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	es, err := NewEncryptionService(true, key)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	// Test data
	testCases := []struct {
		name    string
		content any
	}{
		{"string", "Hello, World!"},
		{"map", map[string]any{"key": "value", "number": 42}},
		{"array", []string{"one", "two", "three"}},
		{"nil", nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encrypt content
			encrypted, err := es.EncryptContent(tc.content)
			if err != nil {
				t.Fatalf("Failed to encrypt content: %v", err)
			}

			// Verify it's encrypted
			if encrypted == tc.content {
				t.Error("Content should be encrypted")
			}

			// Decrypt content
			decrypted, err := es.DecryptContent(encrypted)
			if err != nil {
				t.Fatalf("Failed to decrypt content: %v", err)
			}

			// Verify decryption
			if tc.content == nil {
				if decrypted != nil {
					t.Errorf("Expected nil, got %v", decrypted)
				}
			} else {
				// For complex types, compare JSON representation
				expectedJSON, _ := json.Marshal(tc.content)
				actualJSON, _ := json.Marshal(decrypted)
				if string(expectedJSON) != string(actualJSON) {
					t.Errorf("Decrypted content doesn't match original. Expected: %s, Got: %s",
						string(expectedJSON), string(actualJSON))
				}
			}
		})
	}
}

func TestEncryptedContentStructure(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	es, err := NewEncryptionService(true, key)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	content := "Test message"
	encrypted, err := es.EncryptContent(content)
	if err != nil {
		t.Fatalf("Failed to encrypt content: %v", err)
	}

	// Verify encrypted content structure
	ec, ok := encrypted.(*EncryptedContent)
	if !ok {
		t.Fatalf("Expected EncryptedContent, got %T", encrypted)
	}

	if !ec.Encrypted {
		t.Error("Encrypted flag should be true")
	}

	if ec.Data == "" {
		t.Error("Data should not be empty")
	}

	if ec.Nonce == "" {
		t.Error("Nonce should not be empty")
	}

	// Verify data and nonce are valid base64
	if _, err := base64.StdEncoding.DecodeString(ec.Data); err != nil {
		t.Errorf("Data is not valid base64: %v", err)
	}

	if _, err := base64.StdEncoding.DecodeString(ec.Nonce); err != nil {
		t.Errorf("Nonce is not valid base64: %v", err)
	}
}

func TestDecryptUnencryptedContent(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	es, err := NewEncryptionService(true, key)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	// Test with unencrypted content
	content := "Unencrypted message"
	decrypted, err := es.DecryptContent(content)
	if err != nil {
		t.Fatalf("Failed to decrypt unencrypted content: %v", err)
	}

	if decrypted != content {
		t.Errorf("Expected %s, got %s", content, decrypted)
	}
}

func TestDisabledEncryptionService(t *testing.T) {
	es := &EncryptionService{enabled: false}

	content := "Test message"

	// Encrypt should return content unchanged
	encrypted, err := es.EncryptContent(content)
	if err != nil {
		t.Fatalf("Encrypt should not fail when disabled: %v", err)
	}
	if encrypted != content {
		t.Error("Content should be unchanged when encryption is disabled")
	}

	// Decrypt should return content unchanged
	decrypted, err := es.DecryptContent(content)
	if err != nil {
		t.Fatalf("Decrypt should not fail when disabled: %v", err)
	}
	if decrypted != content {
		t.Error("Content should be unchanged when encryption is disabled")
	}
}
