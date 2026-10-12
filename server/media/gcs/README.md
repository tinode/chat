# GCS Media Handler for Tinode

This package implements the Tinode media handler interface for Google Cloud Storage with KMS encryption.

## Features

- **KMS Encryption**: All uploaded files are automatically encrypted using Google Cloud KMS
- **Automatic File Management**: Handles upload, download, and deletion of files
- **CORS Support**: Configurable CORS headers for web applications
- **Cache Control**: Configurable cache headers for optimal performance
- **Base Path Support**: Optional base path prefix for organizing files in buckets

## Configuration

Add the following configuration to your Tinode server config:

```json
{
  "media": {
    "use_handler": "gcs",
    "handlers":{
      "gcs": {
        "bucket_name": "your-bucket-name",
        "serve_url": "/v0/file/s/",
        "cors_origins": ["*"],
        "cache_control": "max-age=86400",
        "project_id": "your-gcp-project-id",
        "location_id": "your-kms-location",
        "key_ring_id": "your-key-ring-id",
        "key_id": "your-crypto-key-id",
        "base_path": "tinode-files",
        "client_timeout": "30s"
      }
    }
  }
}
```

### Configuration Parameters

- `bucket_name` (required): The GCS bucket name where files will be stored
- `serve_url` (optional): URL path prefix for serving files (default: `/v0/file/s/`)
- `cors_origins` (optional): List of allowed CORS origins (default: `["*"]`)
- `cache_control` (optional): Cache control header value (default: `max-age=86400`)
- `project_id` (required): Your Google Cloud Project ID
- `location_id` (required): The location of your KMS key ring
- `key_ring_id` (required): The ID of your KMS key ring
- `key_id` (required): The ID of your KMS crypto key
- `base_path` (optional): Base path prefix for organizing files in the bucket
- `client_timeout` (optional): Timeout for GCS operations (default: 30s)

## Setup Requirements

1. **Google Cloud Storage**: Create a bucket for storing files
2. **Google Cloud KMS**: Set up a key ring and crypto key for encryption
3. **Service Account**: Create a service account with the following roles:
   - `Storage Object Admin` for bucket operations
   - `Cloud KMS CryptoKey Encrypter/Decrypter` for KMS operations

4. **Authentication**: Ensure your application has proper authentication:
   - Use Application Default Credentials (ADC)
   - Or set the `GOOGLE_APPLICATION_CREDENTIALS` environment variable

## Usage

The handler automatically registers itself with Tinode's media handler registry. Once configured, Tinode will use this handler for all file operations.

### File Upload

Files are uploaded with the following structure:
- Object name: `{file-id}{extension}` (e.g., `abc123def456.jpg`)
- If base_path is configured: `{base-path}/{file-id}{extension}` (e.g., `tinode-files/abc123def456.jpg`)

### File Download

Files are served through Tinode's standard file serving endpoints with proper headers and caching.

### File Deletion

When files are no longer needed, they are automatically deleted from GCS.

## Security

- All files are encrypted at rest using Google Cloud KMS
- Files are served through Tinode's authentication system
- CORS headers are configurable for web security
- No direct public access to GCS bucket (all access through Tinode)

## Performance Considerations

- Files are served through Tinode's HTTP endpoints, not directly from GCS
- Consider using a CDN in front of Tinode for better performance
- The handler doesn't support seeking in files (GCS limitation)
- Large files may take time to upload/download

## Troubleshooting

1. **Authentication Errors**: Ensure your service account has proper permissions
2. **KMS Errors**: Verify your KMS key ring and crypto key exist and are accessible
3. **Bucket Errors**: Ensure the bucket exists and is accessible
4. **Timeout Errors**: Increase the `client_timeout` value for slow connections

## Example Configuration

```json
{
  "media": {
    "use_handler": "gcs",
    "handlers":{
      "gcs": {
        "bucket_name": "my-tinode-files",
        "serve_url": "/v0/file/s/",
        "cors_origins": ["https://myapp.com", "https://www.myapp.com"],
        "cache_control": "max-age=3600",
        "project_id": "my-gcp-project",
        "location_id": "us-central1",
        "key_ring_id": "tinode-keyring",
        "key_id": "tinode-crypto-key",
        "base_path": "uploads",
        "client_timeout": "60s"
      }
    }
  }
}
```

## Implementation Details

The GCS media handler implements the complete Tinode media interface:

- **Init()**: Initializes the GCS client and validates KMS configuration
- **Headers()**: Handles CORS headers and cache management
- **Upload()**: Uploads files to GCS with KMS encryption
- **Download()**: Downloads files from GCS through Tinode's HTTP endpoints
- **Delete()**: Deletes files from GCS when no longer needed
- **GetIdFromUrl()**: Extracts file IDs from Tinode URLs

The handler integrates directly with Google Cloud Storage and uses the official Go client library for all operations.
