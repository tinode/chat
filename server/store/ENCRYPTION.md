# Message Encryption

This document describes the message encryption feature in Tinode, which allows encrypting message content stored in the database to prevent unauthorized access to message content using database tools.

## Overview

The encryption feature uses AES-256-GCM symmetric encryption to encrypt only the `content` field of messages. The encryption is transparent to clients - messages are automatically encrypted when saved and decrypted when retrieved.

## Configuration

### Configuration File

Add encryption settings to your `tinode.conf` file:

```json
{
  "store_config": {
    "encryption": {
      "enabled": true,
      "key": "base64-encoded-32-byte-key-here"
    }
  }
}
```

### Command Line Flags

You can also enable encryption via command line flags:

```bash
./tinode-server --encryption_enabled --encryption_key "base64-encoded-32-byte-key-here"
```

## Key Management

### Generating an Encryption Key

Generate a 32-byte (256-bit) random key using the built-in keygen tool:

```bash
# Generate 32-byte encryption key
cd keygen
./keygen -encryption

# Generate custom size key
./keygen -encryption -keysize 32

# Save key to file
./keygen -encryption -output encryption.key
```

Alternatively, you can use OpenSSL:

```bash
# Generate 32 random bytes and encode in base64
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

### Encryption Algorithm

- **Cipher**: AES-256
- **Mode**: GCM (Galois/Counter Mode)
- **Key size**: 256 bits (32 bytes)
- **Nonce**: Random 12-byte nonce for each message

### Storage Format

Encrypted content is stored as a JSON object:

```json
{
  "data": "base64-encoded-encrypted-data",
  "nonce": "base64-encoded-nonce",
  "encrypted": true
}
```

### Performance Impact

- **Encryption**: ~1-5ms per message (depending on content size)
- **Decryption**: ~1-5ms per message (depending on content size)
- **Storage overhead**: ~33% increase in content field size

## Troubleshooting

### Common Issues

1. **"encryption key is required when encryption is enabled"**
   - Ensure you've provided a valid base64-encoded 32-byte key

2. **"failed to decode base64 encryption key"**
   - Verify your key is properly base64-encoded
   - Ensure the key is exactly 32 bytes when decoded

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

To test encryption functionality:

1. Start the server with encryption enabled
2. Send messages through the normal API
3. Verify messages are encrypted in the database
4. Verify messages are decrypted when retrieved
5. Check that encryption/decryption errors are properly logged
