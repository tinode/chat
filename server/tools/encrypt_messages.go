package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

func main() {
	var (
		configFile = flag.String("config", "tinode.conf", "Path to tinode.conf file")
		keyFile    = flag.String("key", "", "Path to file containing base64-encoded 32-byte encryption key")
		key        = flag.String("key_string", "", "Base64-encoded 32-byte encryption key as string")
		dryRun     = flag.Bool("dry_run", false, "Show what would be done without making changes")
		reverse    = flag.Bool("reverse", false, "Decrypt encrypted messages (use with caution)")
		topicName  = flag.String("topic", "", "Topic name to process (leave empty to process all accessible topics)")
	)
	flag.Parse()

	// Load configuration
	config, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Get encryption key
	var encryptionKey []byte
	if *keyFile != "" {
		keyBytes, err := os.ReadFile(*keyFile)
		if err != nil {
			log.Fatalf("Failed to read key file: %v", err)
		}
		encryptionKey, err = base64.StdEncoding.DecodeString(string(keyBytes))
		if err != nil {
			log.Fatalf("Failed to decode key from file: %v", err)
		}
	} else if *key != "" {
		var err error
		encryptionKey, err = base64.StdEncoding.DecodeString(*key)
		if err != nil {
			log.Fatalf("Failed to decode key string: %v", err)
		}
	} else {
		log.Fatal("Either -key or -key_string must be specified")
	}

	if len(encryptionKey) != 32 {
		log.Fatalf("Encryption key must be exactly 32 bytes, got %d", len(encryptionKey))
	}

	// Initialize store
	if err := store.Store.Open(1, config.Store); err != nil {
		log.Fatalf("Failed to open store: %v", err)
	}
	defer store.Store.Close()

	// Initialize encryption service
	if err := store.InitEncryptionFromFlags(true, *key); err != nil {
		log.Fatalf("Failed to initialize encryption: %v", err)
	}

	// For now, we'll work with a single topic specified by command line
	// In a real implementation, you might want to iterate through all topics
	// or implement a TopicsGetAll method in the adapter

	if *topicName == "" {
		log.Fatal("Please specify a topic name with -topic flag")
	}

	// Get messages for the specified topic
	messages, err := store.Store.GetAdapter().MessageGetAll(*topicName, types.ZeroUid, nil)
	if err != nil {
		log.Fatalf("Failed to get messages for topic %s: %v", *topicName, err)
	}

	totalMessages := len(messages)
	processedMessages := 0
	encryptedMessages := 0
	decryptedMessages := 0
	errors := 0

	for _, msg := range messages {
		if *reverse {
			// Decrypt encrypted messages
			if isEncrypted(msg.Content) {
				decryptedContent, err := store.GetEncryptionService().DecryptContent(msg.Content)
				if err != nil {
					log.Printf("Failed to decrypt message %s in topic %s: %v", msg.Uid(), *topicName, err)
					errors++
					continue
				}
				msg.Content = decryptedContent
				decryptedMessages++
			}
		} else {
			// Encrypt unencrypted messages
			if !isEncrypted(msg.Content) {
				encryptedContent, err := store.GetEncryptionService().EncryptContent(msg.Content)
				if err != nil {
					log.Printf("Failed to encrypt message %s in topic %s: %v", msg.Uid(), *topicName, err)
					errors++
					continue
				}
				msg.Content = encryptedContent
				encryptedMessages++
			}
		}

		if !*dryRun {
			// Save the modified message
			if err := store.Store.GetAdapter().MessageSave(&msg); err != nil {
				log.Printf("Failed to save message %s in topic %s: %v", msg.Uid(), *topicName, err)
				errors++
				continue
			}
		}

		processedMessages++
	}

	if *reverse {
		fmt.Printf("Decryption complete:\n")
		fmt.Printf("  Total messages: %d\n", totalMessages)
		fmt.Printf("  Processed: %d\n", processedMessages)
		fmt.Printf("  Decrypted: %d\n", decryptedMessages)
	} else {
		fmt.Printf("Encryption complete:\n")
		fmt.Printf("  Total messages: %d\n", totalMessages)
		fmt.Printf("  Processed: %d\n", processedMessages)
		fmt.Printf("  Encrypted: %d\n", encryptedMessages)
	}
	fmt.Printf("  Errors: %d\n", errors)

	if *dryRun {
		fmt.Printf("\nThis was a dry run. No changes were made.\n")
	}
}

// loadConfig loads configuration from file
func loadConfig(configFile string) (*configType, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var config configType
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// configType represents the configuration structure
type configType struct {
	Store json.RawMessage `json:"store_config"`
}

// isEncrypted checks if message content is encrypted
func isEncrypted(content any) bool {
	if content == nil {
		return false
	}

	// Check if it's an EncryptedContent struct
	if _, ok := content.(*store.EncryptedContent); ok {
		return true
	}

	// Check if it's a JSON string that represents EncryptedContent
	if contentStr, ok := content.(string); ok {
		var ec store.EncryptedContent
		if err := json.Unmarshal([]byte(contentStr), &ec); err == nil {
			return ec.Encrypted
		}
	}

	return false
}
