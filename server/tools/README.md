# Message Encryption at Rest Tools

This directory contains tools for managing message encryption at rest in Tinode.

## Key Generation Tool

Generate encryption keys for message encryption at rest using the main keygen tool:

```bash
# Generate a 32-byte key (AES-256) and display it
cd ../../keygen
./keygen -encrypt_at_rest

# Generate a 16-byte key (AES-128)
./keygen -encrypt_at_rest -keysize 16

# Generate a 24-byte key (AES-192)
./keygen -encrypt_at_rest -keysize 24

# Save key to file using shell redirection
./keygen -encrypt_at_rest > encrypt_at_rest.key
```

**Note:** The keygen tool is located in the root `keygen/` directory, not in `server/tools/`.

## Message Encryption Migration Tool

Encrypt or decrypt existing messages in the database:

```bash
# Encrypt messages in a topic (dry run first)
go run encrypt_messages.go \
  --config tinode.conf \
  --key_string "your-base64-encoded-key" \
  --topic "your-topic-name" \
  --dry_run

# Actually encrypt the messages
go run encrypt_messages.go \
  --config tinode.conf \
  --key_string "your-base64-encoded-key" \
  --topic "your-topic-name"

# Decrypt messages (use with caution)
go run encrypt_messages.go \
  --config tinode.conf \
  --key_string "your-base64-encoded-key" \
  --topic "your-topic-name" \
  --reverse
```

## Usage Examples

### 1. Generate an Encryption Key

```bash
cd keygen
./keygen -encrypt_at_rest
```

This will output something like:
```
Generated 32-byte encryption key:
dGVzdGtleXRlc3RrZXl0ZXN0a2V5dGVzdGtleXRlc3Q=

Add this to your tinode.conf:
"encrypt_at_rest": {
    "key": "dGVzdGtleXRlc3RrZXl0ZXN0a2V5dGVzdGtleXRlc3Q="
}
```

### 2. Enable Encryption at Rest in Configuration

Add the `encrypt_at_rest` section to your `tinode.conf`:

```json
{
  "store_config": {
    "encrypt_at_rest": {
      "key": "your-generated-key-here"
    }
  }
}
```

### 3. Encrypt Existing Messages

```bash
# First, do a dry run to see what would be encrypted
go run encrypt_messages.go \
  --config /path/to/tinode.conf \
  --key_string "your-key" \
  --topic "usr123" \
  --dry_run

# Then run the actual encryption
go run encrypt_messages.go \
  --config /path/to/tinode.conf \
  --key_string "your-key" \
  --topic "usr123"
```

## Security Notes

- Keep your encryption keys secure and never commit them to version control
- Use the `--dry_run` flag to test before making changes
- Backup your database before running encryption/decryption tools
- The `--reverse` flag decrypts messages - use with extreme caution
- Consider using file-based keys for better security: `--key /path/to/keyfile`

## Troubleshooting

### Common Issues

1. **"Please specify a topic name with -topic flag"**
   - The tool requires a specific topic name for now
   - Use the topic ID (e.g., "usr123" for user topics, "grp456" for group topics)

2. **"Failed to decode base64 encryption key"**
   - Ensure your key is properly base64-encoded
   - Use the key generation tool to create valid keys

3. **"Encryption key must be 16 (AES-128), 24 (AES-192), or 32 (AES-256) bytes"**
   - Your key must decode to exactly 16, 24, or 32 bytes
   - Use the keygen tool with `-encrypt_at_rest` to generate valid keys

4. **"Failed to get messages for topic"**
   - Verify the topic name exists
   - Check that your configuration file is correct
   - Ensure the database connection is working

### Performance Considerations

- Large topics with many messages may take time to process
- Consider processing during low-traffic periods
- Monitor database performance during encryption/decryption
