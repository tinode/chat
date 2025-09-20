package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// EncryptionService handles encryption and decryption of message content
type EncryptionService struct {
	enabled bool
	key     []byte
	aead    cipher.AEAD
}

// EncryptedContent represents encrypted message content with metadata
type EncryptedContent struct {
	Data      string `json:"data"`      // Base64 encoded encrypted data
	Nonce     string `json:"nonce"`     // Base64 encoded nonce
	Encrypted bool   `json:"encrypted"` // Flag to identify encrypted content
}

// NewEncryptionService creates a new encryption service
func NewEncryptionService(enabled bool, key []byte) (*EncryptionService, error) {
	if !enabled {
		return &EncryptionService{enabled: false}, nil
	}

	if len(key) == 0 {
		return nil, fmt.Errorf("encryption key cannot be empty when encryption is enabled")
	}

	// Ensure key is exactly 32 bytes for AES-256
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be exactly 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM mode: %w", err)
	}

	return &EncryptionService{
		enabled: true,
		key:     key,
		aead:    aead,
	}, nil
}

// IsEnabled returns whether encryption is enabled
func (es *EncryptionService) IsEnabled() bool {
	return es.enabled
}

// EncryptContent encrypts message content
func (es *EncryptionService) EncryptContent(content any) (any, error) {
	if !es.enabled {
		return content, nil
	}

	// Convert content to JSON bytes
	contentBytes, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal content to JSON: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, es.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt the content
	encrypted := es.aead.Seal(nil, nonce, contentBytes, nil)

	// Create encrypted content structure
	encryptedContent := &EncryptedContent{
		Data:      base64.StdEncoding.EncodeToString(encrypted),
		Nonce:     base64.StdEncoding.EncodeToString(nonce),
		Encrypted: true,
	}

	return encryptedContent, nil
}

// DecryptContent decrypts message content
func (es *EncryptionService) DecryptContent(content any) (any, error) {
	if !es.enabled {
		return content, nil
	}

	// Check if content is encrypted
	encryptedContent, ok := content.(*EncryptedContent)
	if !ok {
		// Try to unmarshal from JSON if it's a string
		if contentStr, ok := content.(string); ok {
			var ec EncryptedContent
			if err := json.Unmarshal([]byte(contentStr), &ec); err == nil && ec.Encrypted {
				encryptedContent = &ec
			} else {
				// Not encrypted, return as-is
				return content, nil
			}
		} else {
			// Not encrypted, return as-is
			return content, nil
		}
	}

	if !encryptedContent.Encrypted {
		return content, nil
	}

	// Decode base64 data and nonce
	encryptedData, err := base64.StdEncoding.DecodeString(encryptedContent.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted data: %w", err)
	}

	nonce, err := base64.StdEncoding.DecodeString(encryptedContent.Nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to decode nonce: %w", err)
	}

	// Decrypt the content
	decryptedBytes, err := es.aead.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt content: %w", err)
	}

	// Try to unmarshal back to original type
	var decryptedContent any
	if err := json.Unmarshal(decryptedBytes, &decryptedContent); err != nil {
		// If unmarshaling fails, return the raw bytes as string
		return string(decryptedBytes), nil
	}

	return decryptedContent, nil
}

// GetKey returns a copy of the encryption key (for testing purposes)
func (es *EncryptionService) GetKey() []byte {
	if !es.enabled {
		return nil
	}
	keyCopy := make([]byte, len(es.key))
	copy(keyCopy, es.key)
	return keyCopy
}

// GetEncryptionService returns the current encryption service instance
func GetEncryptionService() *EncryptionService {
	return encryptionService
}

// IsEncryptionEnabled returns whether encryption is currently enabled
func IsEncryptionEnabled() bool {
	return encryptionService != nil && encryptionService.IsEnabled()
}
