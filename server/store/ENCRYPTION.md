# Message Encryption at Rest

This document describes the message encryption at rest feature in Tinode, which allows encrypting message content stored in the database to prevent unauthorized access to message content using database tools.

## Overview

The encryption at rest feature uses AES-GCM symmetric encryption to encrypt only the `content` field of messages. The encryption is transparent to clients - messages are automatically encrypted when saved and decrypted when retrieved.

**Supported AES key sizes:**

- AES-128: 16 bytes (128 bits)
- AES-192: 24 bytes (192 bits)
- AES-256: 32 bytes (256 bits)

## Configuration

### Configuration File

Add the `encrypt_at_rest` settings to your `tinode.conf` file:

```json
{
  "store_config": {
    "encrypt_at_rest": {
      "key": "base64-encoded-key-here"
    }
  }
}
```

**Note:** If no key is provided or the key is empty, encryption at rest is disabled.

## Key Management

### Generating a Key

Generate a random key using the built-in keygen tool:

```bash
# Generate 32-byte key (AES-256)
cd keygen
./keygen -encrypt_at_rest

# Generate 16-byte key (AES-128)
./keygen -encrypt_at_rest -keysize 16

# Generate 24-byte key (AES-192)
./keygen -encrypt_at_rest -keysize 24

# Save key to file using shell redirection
./keygen -encrypt_at_rest > encrypt_at_rest.key
```

The keygen tool validates that the key size is exactly 16, 24, or 32 bytes.

Alternatively, you can use OpenSSL:

```bash
# Generate 16 random bytes and encode in base64 (AES-128)
openssl rand -base64 16

# Generate 24 random bytes and encode in base64 (AES-192)
openssl rand -base64 24

# Generate 32 random bytes and encode in base64 (AES-256)
openssl rand -base64 32
```

### Key Storage

Store your encryption key securely:
- Never commit encryption keys to version control
- Use environment variables or secure key management systems
- Consider using hardware security modules (HSMs) for production environments

## Migration

### From Unencrypted to Encrypted

To encrypt existing unencrypted messages, use the migration tool:

```bash
# First, do a dry run to see what would be encrypted
go run server/tools/encrypt_messages.go \
  --config tinode.conf \
  --key_string "your-base64-encoded-key" \
  --topic "your-topic-name" \
  --dry_run

# Then run the actual encryption
go run server/tools/encrypt_messages.go \
  --config tinode.conf \
  --key_string "your-base64-encoded-key" \
  --topic "your-topic-name"
```

**Note:** The migration tool uses the adapter directly to bypass auto-encryption/decryption and handles all supported AES key sizes.

### From Encrypted to Unencrypted

To decrypt encrypted messages (use with caution):

```bash
go run server/tools/encrypt_messages.go \
  --config tinode.conf \
  --key_string "your-base64-encoded-key" \
  --topic "your-topic-name" \
  --reverse
```

## Security Considerations

### What is Encrypted

- **Message content**: The actual text/content of messages
- **Metadata**: Message headers, timestamps, sender info, etc. remain unencrypted

### What is NOT Encrypted

- Message metadata (sender, timestamp, sequence ID, etc.)
- Topic information
- User information
- File attachments (planned for future versions)

### Limitations

- Database administrators can still see message metadata
- The encryption key must be stored securely
- If the key is lost, encrypted messages cannot be recovered
- Encryption adds computational overhead

## Implementation Details

### Algorithm

- **Cipher**: AES (Advanced Encryption Standard)
- **Mode**: GCM (Galois/Counter Mode)
- **Key sizes**: 128, 192, or 256 bits (16, 24, or 32 bytes)
- **Nonce**: Random 12-byte nonce for each message

### Storage Format

Encrypted content is stored as a JSON object with automatic base64 encoding:

```json
{
  "data": "base64-encoded-encrypted-data",
  "nonce": "base64-encoded-nonce", 
  "encrypted": true
}
```

The `data` and `nonce` fields are automatically base64 encoded/decoded during JSON marshaling/unmarshaling.

### Performance Impact

- **Encryption**: ~1-5ms per message (depending on content size)
- **Decryption**: ~1-5ms per message (depending on content size)
- **Storage overhead**: ~33% increase in content field size

## Troubleshooting

### Common Issues

1. **"encryption key must be 16, 24, or 32 bytes"**
   - Ensure your key is exactly 16, 24, or 32 bytes when decoded from base64
   - Use the keygen tool to generate valid keys

2. **"failed to decode base64 encryption key"**
   - Verify your key is properly base64-encoded
   - Ensure there are no extra spaces or newlines in the key

3. **"failed to create AES cipher"**
   - This usually indicates a system-level issue with crypto libraries

### Logs

Encryption-related errors and warnings are logged with the prefix:
- `topic[topic-name]: failed to encrypt message content (seq: X) - err: ...`
- `topic[topic-name]: failed to decrypt message content (seq: X) - err: ...`

## Future Enhancements

- File attachment encryption
- Key rotation support
- Hardware security module (HSM) integration
- Per-topic encryption settings
- End-to-end encryption support

## API Changes

No changes to the client API are required. Messages are automatically encrypted/decrypted transparently.

## Testing

To test the encryption at rest functionality:

1. Start the server with `encrypt_at_rest` configured
2. Send messages through the normal API
3. Verify messages are encrypted in the database
4. Verify messages are decrypted when retrieved
5. Check that encryption/decryption errors are properly logged
