# Encryption Tools

This directory contains tools for managing message encryption in Tinode.

## Key Generation Tool

Generate encryption keys for message encryption using the main keygen tool:

```bash
# Generate a 32-byte key and display it
cd ../../keygen
./keygen -encryption

# Generate a key and save it to a file
./keygen -encryption -output encryption.key

# Generate a custom size key (default is 32 bytes for AES-256)
./keygen -encryption -keysize 32
```

**Note:** The keygen tool is located in the root `keygen/` directory, not in `server/tools/`.

## Message Encryption Tool

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
./keygen -encryption
```

This will output something like:
```
Generated 32-byte encryption key:
dGVzdGtleXRlc3RrZXl0ZXN0a2V5dGVzdGtleXRlc3Q=

Add this to your tinode.conf:
"encryption": {
    "enabled": true,
    "key": "dGVzdGtleXRlc3RrZXl0ZXN0a2V5dGVzdGtleXRlc3Q="
}
```

### 2. Enable Encryption in Configuration

Add the encryption section to your `tinode.conf`:

```json
{
  "store_config": {
    "encryption": {
      "enabled": true,
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

3. **"Failed to get messages for topic"**
   - Verify the topic name exists
   - Check that your configuration file is correct
   - Ensure the database connection is working

### Performance Considerations

- Large topics with many messages may take time to process
- Consider processing during low-traffic periods
- Monitor database performance during encryption/decryption
