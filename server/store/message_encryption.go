package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
)

// MessageEncryptionService handles encryption and decryption of message content at rest
type MessageEncryptionService struct {
	key  []byte
	aead cipher.AEAD
}

// EncryptedContent represents encrypted message content with metadata
type EncryptedContent struct {
	Data      []byte `json:"data"`      // Encrypted data (automatically base64 encoded/decoded)
	Nonce     []byte `json:"nonce"`     // Nonce (automatically base64 encoded/decoded)
	Encrypted bool   `json:"encrypted"` // Flag to identify encrypted content
}

// NewMessageEncryptionService creates a new message encryption service
func NewMessageEncryptionService(key []byte) (*MessageEncryptionService, error) {
	if len(key) == 0 {
		return nil, nil // No encryption if no key provided
	}

	// Validate key size - AES supports 16, 24, or 32 bytes
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 16, 24, or 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM mode: %w", err)
	}

	return &MessageEncryptionService{
		key:  key,
		aead: aead,
	}, nil
}

// IsEnabled returns whether encryption is enabled (key is present)
func (es *MessageEncryptionService) IsEnabled() bool {
	return es != nil && es.key != nil
}

// EncryptContent encrypts message content
func (es *MessageEncryptionService) EncryptContent(content any) (any, error) {
	if es == nil || es.key == nil {
		return content, nil
	}

	// Handle nil content
	if content == nil {
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
		Data:      encrypted,
		Nonce:     nonce,
		Encrypted: true,
	}

	return encryptedContent, nil
}

// DecryptContent decrypts message content
func (es *MessageEncryptionService) DecryptContent(content any) (any, error) {
	if es == nil || es.key == nil {
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

	// Decrypt the content (Data and Nonce are already []byte, no need to decode)
	decryptedBytes, err := es.aead.Open(nil, encryptedContent.Nonce, encryptedContent.Data, nil)
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
func (es *MessageEncryptionService) GetKey() []byte {
	if es == nil || es.key == nil {
		return nil
	}
	keyCopy := make([]byte, len(es.key))
	copy(keyCopy, es.key)
	return keyCopy
}

// GetMessageEncryptionService returns the current message encryption service instance
func GetMessageEncryptionService() *MessageEncryptionService {
	return messageEncryptionService
}

// IsMessageEncryptionEnabled returns whether message encryption is currently enabled
func IsMessageEncryptionEnabled() bool {
	return messageEncryptionService != nil && messageEncryptionService.IsEnabled()
}
