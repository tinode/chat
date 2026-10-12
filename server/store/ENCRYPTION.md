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

### New Deployments

For new deployments, simply configure the `encrypt_at_rest` key before starting the server. All new messages will be encrypted automatically.

### Existing Deployments

**Important:** Enabling encryption on an existing deployment with unencrypted messages requires careful planning.

#### Mixed Content Handling

The decryption logic automatically handles mixed content:

- **Encrypted messages**: Will be decrypted using the configured key
- **Unencrypted messages**: Will be returned as-is (no modification needed)

This means you can safely enable encryption on an existing deployment:

1. Configure the `encrypt_at_rest` key in `tinode.conf`
2. Restart the server
3. New messages will be encrypted; old messages remain readable

#### Full Migration (Optional)

If you require all messages to be encrypted (for compliance or security reasons), you will need to write a custom migration script that:

1. Reads messages directly from the database
2. Encrypts the `content` field using the same AES-GCM algorithm
3. Updates the messages in the database

**Note:** A built-in migration tool is planned for a future release.

### Key Rotation

Key rotation is not currently supported. If you need to change the encryption key:

1. Decrypt all messages using the old key (requires custom script)
2. Update the configuration with the new key
3. Re-encrypt all messages with the new key

**Note:** Key rotation support is planned for a future release.

## Security Considerations

### What is Encrypted

- **Message content**: The actual text/content of messages

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
  "nonce": "base64-encoded-nonce"
}
```

The `data` and `nonce` fields are automatically base64 encoded/decoded during JSON marshaling/unmarshaling.

### Performance Impact

- **Encryption**: ~1-5ms per message (depending on content size)
- **Decryption**: ~1-5ms per message (depending on content size)
- **Storage overhead**: ~33% increase in content field size

## Troubleshooting


## Testing

To test the encryption at rest functionality:

1. Start the server with `encrypt_at_rest` configured
2. Send messages through the normal API
3. Verify messages are encrypted in the database
4. Verify messages are decrypted when retrieved
5. Check that encryption/decryption errors are properly logged
