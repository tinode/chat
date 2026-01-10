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

			// Simulate DB round-trip: marshal to JSON, unmarshal to map[string]any
			if tc.content != nil {
				jsonBytes, _ := json.Marshal(encrypted)
				var mapContent map[string]any
				json.Unmarshal(jsonBytes, &mapContent)
				encrypted = mapContent
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

	if len(ec.Data) == 0 {
		t.Error("Data should not be empty")
	}

	if len(ec.Nonce) == 0 {
		t.Error("Nonce should not be empty")
	}
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

// TestDecryptContentFromDatabaseFormat tests decryption of content that has been
// through a database round-trip (JSON marshal -> DB -> JSON unmarshal as map[string]any)
func TestDecryptContentFromDatabaseFormat(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	es, err := NewMessageEncryptionService(key)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	testCases := []struct {
		name    string
		content any
	}{
		{"string", "Test message for database round-trip"},
		{"map", map[string]any{"message": "hello", "count": 42}},
		{"array", []any{"one", "two", "three"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encrypt content
			encrypted, err := es.EncryptContent(tc.content)
			if err != nil {
				t.Fatalf("Failed to encrypt content: %v", err)
			}

			// Simulate DB round-trip: marshal to JSON, then unmarshal to map[string]any
			jsonBytes, err := json.Marshal(encrypted)
			if err != nil {
				t.Fatalf("Failed to marshal encrypted content: %v", err)
			}

			var mapContent map[string]any
			if err := json.Unmarshal(jsonBytes, &mapContent); err != nil {
				t.Fatalf("Failed to unmarshal to map: %v", err)
			}

			// Now decrypt from map[string]any format (as it would come from DB)
			decrypted, err := es.DecryptContent(mapContent)
			if err != nil {
				t.Fatalf("Failed to decrypt from map format: %v", err)
			}

			// Verify decryption matches original
			expectedJSON, _ := json.Marshal(tc.content)
			actualJSON, _ := json.Marshal(decrypted)
			if string(expectedJSON) != string(actualJSON) {
				t.Errorf("Decrypted content doesn't match original. Expected: %s, Got: %s",
					string(expectedJSON), string(actualJSON))
			}
		})
	}
}

// TestDecryptContentFromMapWithInvalidData tests error handling for malformed encrypted content
func TestDecryptContentFromMapWithInvalidData(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	es, err := NewMessageEncryptionService(key)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	// Test with map missing nonce - should return as-is
	mapNoNonce := map[string]any{"data": "test"}
	result, err := es.DecryptContent(mapNoNonce)
	if err != nil {
		t.Errorf("Should not error on map without nonce: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok || resultMap["data"] != "test" {
		t.Error("Should return original map when nonce is missing")
	}

	// Test with map missing data - should return as-is
	mapNoData := map[string]any{"nonce": "test"}
	result, err = es.DecryptContent(mapNoData)
	if err != nil {
		t.Errorf("Should not error on map without data: %v", err)
	}
	resultMap, ok = result.(map[string]any)
	if !ok || resultMap["nonce"] != "test" {
		t.Error("Should return original map when data is missing")
	}

	// Test with invalid base64 data - should return as-is (graceful handling)
	mapBadData := map[string]any{"data": "not-valid-base64!!!", "nonce": "also-bad!!!"}
	result, err = es.DecryptContent(mapBadData)
	if err != nil {
		t.Errorf("Should not error on invalid base64, should return as-is: %v", err)
	}
	resultMap, ok = result.(map[string]any)
	if !ok || resultMap["data"] != "not-valid-base64!!!" {
		t.Error("Should return original map when base64 decoding fails")
	}
}

// TestEncryptedContentFromMapHelper tests the encryptedContentFromMap helper function
func TestEncryptedContentFromMapHelper(t *testing.T) {
	// Valid encrypted content map
	validMap := map[string]any{
		"data":  "dGVzdCBkYXRh",     // "test data" in base64
		"nonce": "dGVzdCBub25jZQ==", // "test nonce" in base64
	}

	ec, err := encryptedContentFromMap(validMap)
	if err != nil {
		t.Fatalf("Failed to convert valid map: %v", err)
	}
	if string(ec.Data) != "test data" {
		t.Errorf("Data mismatch: got %s", string(ec.Data))
	}
	if string(ec.Nonce) != "test nonce" {
		t.Errorf("Nonce mismatch: got %s", string(ec.Nonce))
	}

	// Missing data field
	_, err = encryptedContentFromMap(map[string]any{"nonce": "dGVzdA=="})
	if err == nil {
		t.Error("Should fail when data field is missing")
	}

	// Missing nonce field
	_, err = encryptedContentFromMap(map[string]any{"data": "dGVzdA=="})
	if err == nil {
		t.Error("Should fail when nonce field is missing")
	}

	// Invalid base64 in data
	_, err = encryptedContentFromMap(map[string]any{"data": "!!!invalid!!!", "nonce": "dGVzdA=="})
	if err == nil {
		t.Error("Should fail on invalid base64 in data")
	}

	// Invalid base64 in nonce
	_, err = encryptedContentFromMap(map[string]any{"data": "dGVzdA==", "nonce": "!!!invalid!!!"})
	if err == nil {
		t.Error("Should fail on invalid base64 in nonce")
	}
}
