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

// MessageEncryptionService handles encryption and decryption of message content at rest
type MessageEncryptionService struct {
	key  []byte
	aead cipher.AEAD
}

// EncryptedContent represents encrypted message content with metadata
type EncryptedContent struct {
	Data  []byte `json:"data"`  // Encrypted data (automatically base64 encoded/decoded)
	Nonce []byte `json:"nonce"` // Nonce (automatically base64 encoded/decoded)
}

// NewMessageEncryptionService creates a new message encryption service
func NewMessageEncryptionService(key []byte) (*MessageEncryptionService, error) {
	if len(key) == 0 {
		return nil, nil // No encryption if no key provided
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
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
	return &EncryptedContent{
		Data:  encrypted,
		Nonce: nonce,
	}, nil
}

// DecryptContent decrypts message content
func (es *MessageEncryptionService) DecryptContent(content any) (any, error) {
	if es == nil || es.key == nil {
		return content, nil
	}

	// Content from database comes as map[string]any
	contentMap, ok := content.(map[string]any)
	if !ok {
		// Not in expected format, assume not encrypted
		return content, nil
	}

	// Detect encrypted content by presence of data and nonce fields
	ec, err := encryptedContentFromMap(contentMap)
	if err != nil {
		// Not valid encrypted content, return as-is
		return content, nil
	}

	// Decrypt the content
	decryptedBytes, err := es.aead.Open(nil, ec.Nonce, ec.Data, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt content: %w", err)
	}

	// Unmarshal back to original type
	var decryptedContent any
	if err := json.Unmarshal(decryptedBytes, &decryptedContent); err != nil {
		return string(decryptedBytes), nil
	}

	return decryptedContent, nil
}

// encryptedContentFromMap converts a map[string]any to EncryptedContent
// This handles the case where content is read from the database and unmarshaled as a map
func encryptedContentFromMap(m map[string]any) (*EncryptedContent, error) {
	ec := &EncryptedContent{}

	// Get and decode the data field (base64 encoded string from JSON)
	if dataStr, ok := m["data"].(string); ok {
		data, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode data: %w", err)
		}
		ec.Data = data
	} else {
		return nil, fmt.Errorf("missing or invalid 'data' field")
	}

	// Get and decode the nonce field (base64 encoded string from JSON)
	if nonceStr, ok := m["nonce"].(string); ok {
		nonce, err := base64.StdEncoding.DecodeString(nonceStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode nonce: %w", err)
		}
		ec.Nonce = nonce
	} else {
		return nil, fmt.Errorf("missing or invalid 'nonce' field")
	}

	return ec, nil
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
