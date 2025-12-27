package store

import (
	"encoding/json"
	"testing"
)

func TestNewMessageEncryptionService(t *testing.T) {
	// Test with no key (disabled encryption)
	es, err := NewMessageEncryptionService(nil)
	if err != nil {
		t.Fatalf("Failed to create disabled encryption service: %v", err)
	}
	if es != nil {
		t.Error("Encryption service should be nil when no key provided")
	}

	// Test with empty key (disabled encryption)
	es, err = NewMessageEncryptionService([]byte{})
	if err != nil {
		t.Fatalf("Failed to create disabled encryption service: %v", err)
	}
	if es != nil {
		t.Error("Encryption service should be nil when empty key provided")
	}

	// Test with valid 32-byte key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	es, err = NewMessageEncryptionService(key)
	if err != nil {
		t.Fatalf("Failed to create enabled encryption service: %v", err)
	}
	if es == nil || !es.IsEnabled() {
		t.Error("Encryption service should be enabled")
	}

	// Test with valid 16-byte key
	key16 := make([]byte, 16)
	for i := range key16 {
		key16[i] = byte(i)
	}
	es, err = NewMessageEncryptionService(key16)
	if err != nil {
		t.Fatalf("Failed to create enabled encryption service with 16-byte key: %v", err)
	}
	if es == nil || !es.IsEnabled() {
		t.Error("Encryption service should be enabled with 16-byte key")
	}

	// Test with valid 24-byte key
	key24 := make([]byte, 24)
	for i := range key24 {
		key24[i] = byte(i)
	}
	es, err = NewMessageEncryptionService(key24)
	if err != nil {
		t.Fatalf("Failed to create enabled encryption service with 24-byte key: %v", err)
	}
	if es == nil || !es.IsEnabled() {
		t.Error("Encryption service should be enabled with 24-byte key")
	}

	// Test with invalid key length
	_, err = NewMessageEncryptionService([]byte("short"))
	if err == nil {
		t.Error("Should fail with short key")
	}
}

func TestEncryptDecryptContent(t *testing.T) {
	// Create encryption service
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	es, err := NewMessageEncryptionService(key)
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

			// Verify it's encrypted (except for nil content which stays nil)
			if tc.content != nil && encrypted == tc.content {
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
	es, err := NewMessageEncryptionService(key)
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

	if len(ec.Data) == 0 {
		t.Error("Data should not be empty")
	}

	if len(ec.Nonce) == 0 {
		t.Error("Nonce should not be empty")
	}

	// Data and Nonce are now []byte, so they don't need base64 validation
	// The JSON marshaling/unmarshaling handles base64 conversion automatically
}

func TestDecryptUnencryptedContent(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	es, err := NewMessageEncryptionService(key)
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
	// Test with nil service (no encryption)
	var es *MessageEncryptionService = nil

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
