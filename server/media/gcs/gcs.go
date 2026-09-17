// Package gcs implements github.com/tinode/chat/server/media interface by storing media objects in Google Cloud Storage.
package gcs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cloud.google.com/go/storage"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/media"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	defaultServeURL     = "/v0/file/s/"
	defaultCacheControl = "max-age=86400"
	handlerName         = "gcs"
)

type configType struct {
	BucketName    string         `json:"bucket_name"`
	ServeURL      string         `json:"serve_url"`
	CorsOrigins   []string       `json:"cors_origins"`
	CacheControl  string         `json:"cache_control"`
	ProjectID     string         `json:"project_id"`
	LocationID    string         `json:"location_id"`
	KeyRingID     string         `json:"key_ring_id"`
	KeyID         string         `json:"key_id"`
	BasePath      string         `json:"base_path"`
	ClientTimeout *time.Duration `json:"client_timeout"`
}

type gcshandler struct {
	bucketName   string
	serveURL     string
	corsOrigins  []string
	cacheControl string
	projectID    string
	locationID   string
	keyRingID    string
	keyID        string
	basePath     string
	client       *storage.Client
}

func (gh *gcshandler) Init(jsconf string) error {
	var err error
	var config configType

	if err = json.Unmarshal([]byte(jsconf), &config); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	gh.bucketName = config.BucketName
	if gh.bucketName == "" {
		return errors.New("missing bucket name")
	}

	gh.serveURL = config.ServeURL
	if gh.serveURL == "" {
		gh.serveURL = defaultServeURL
	}

	gh.cacheControl = config.CacheControl
	if gh.cacheControl == "" {
		gh.cacheControl = defaultCacheControl
	}

	gh.corsOrigins = config.CorsOrigins
	gh.projectID = config.ProjectID
	gh.locationID = config.LocationID
	gh.keyRingID = config.KeyRingID
	gh.keyID = config.KeyID
	gh.basePath = config.BasePath

	// Validate KMS configuration
	if gh.projectID == "" || gh.locationID == "" || gh.keyRingID == "" || gh.keyID == "" {
		return errors.New("missing required KMS configuration (project_id, location_id, key_ring_id, key_id)")
	}

	// Initialize GCS client
	ctx := context.Background()
	gh.client, err = storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create storage client: %w", err)
	}

	return nil
}

// Headers is used for cache management and serving CORS headers.
func (gh *gcshandler) Headers(method string, url *url.URL, headers http.Header, serve bool) (http.Header, int, error) {
	if method == http.MethodGet {
		fid := gh.GetIdFromUrl(url.String())
		if fid.IsZero() {
			return nil, 0, types.ErrNotFound
		}

		fdef, err := gh.getFileRecord(fid)
		if err != nil {
			return nil, 0, err
		}

		if etag := strings.Trim(headers.Get("If-None-Match"), "\""); etag != "" && etag == fdef.ETag {
			// Create headers with CORS support
			responseHeaders := http.Header{
				"Last-Modified": {fdef.UpdatedAt.Format(http.TimeFormat)},
				"ETag":          {`"` + fdef.ETag + `"`},
				"Cache-Control": {gh.cacheControl},
			}

			// Add CORS headers for all responses
			responseHeaders.Set("Access-Control-Allow-Origin", "*")
			responseHeaders.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			responseHeaders.Set("Access-Control-Allow-Headers", "*")

			return responseHeaders, http.StatusNotModified, nil
		}

		// Create headers with CORS support
		responseHeaders := http.Header{
			"Content-Type":  {fdef.MimeType},
			"Cache-Control": {gh.cacheControl},
			"ETag":          {`"` + fdef.ETag + `"`},
		}

		// Add CORS headers for all responses
		responseHeaders.Set("Access-Control-Allow-Origin", "*")
		responseHeaders.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		responseHeaders.Set("Access-Control-Allow-Headers", "*")

		return responseHeaders, 0, nil
	}

	if method != http.MethodOptions {
		// Not an OPTIONS request. No special handling for all other requests.
		return nil, 0, nil
	}

	// Ensure CORS origins are set, default to "*" if not configured
	corsOrigins := gh.corsOrigins
	if len(corsOrigins) == 0 {
		corsOrigins = []string{"*"}
	}

	header, status := media.CORSHandler(method, headers, corsOrigins, serve)

	// Ensure Tinode-specific headers are allowed for preflight requests
	if method == http.MethodOptions && status == http.StatusNoContent {
		if header == nil {
			header = http.Header{}
		}
		// The media.CORSHandler already sets Access-Control-Allow-Headers to "*"
		// but let's be explicit about Tinode headers for clarity
		if header.Get("Access-Control-Allow-Headers") == "*" {
			// Keep the wildcard, it's correct
		} else {
			// Fallback to explicit headers if needed
			header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, X-Tinode-APIKey, X-Tinode-Auth")
		}
	}

	return header, status, nil
}

// Upload processes request for file upload. The file is given as io.Reader.
func (gh *gcshandler) Upload(fdef *types.FileDef, file io.Reader) (string, int64, error) {
	// Generate object name using file ID
	objectName := fdef.Uid().String32()

	// Add file extension if available
	ext, _ := mime.ExtensionsByType(fdef.MimeType)
	if len(ext) > 0 {
		objectName += ext[0]
	}

	// Add base path if configured
	if gh.basePath != "" {
		objectName = gh.basePath + "/" + objectName
	}

	// Start upload record in database
	if err := store.Files.StartUpload(fdef); err != nil {
		logs.Warn.Println("failed to create file record", fdef.Id, err)
		return "", 0, err
	}

	// Upload to GCS with KMS encryption
	ctx := context.Background()
	err := gh.uploadFileWithKMS(ctx, gh.bucketName, objectName, file)
	if err != nil {
		logs.Warn.Println("Upload: failed to upload file to GCS", objectName, err)
		return "", 0, err
	}

	// Get file size
	size, err := gh.getFileSize(ctx, gh.bucketName, objectName)
	if err != nil {
		logs.Warn.Println("Upload: failed to get file size", objectName, err)
		return "", 0, err
	}

	// Generate ETag (using object name as it's unique)
	fdef.Location = objectName
	fdef.ETag = etagFromObjectName(objectName)

	// Generate serve URL
	serveURL := gh.serveURL + fdef.Id
	if len(ext) > 0 {
		serveURL += ext[0]
	}

	return serveURL, size, nil
}

// Download processes request for file download.
// The returned ReadSeekCloser must be closed after use.
func (gh *gcshandler) Download(url string) (*types.FileDef, media.ReadSeekCloser, error) {
	fid := gh.GetIdFromUrl(url)
	if fid.IsZero() {
		return nil, nil, types.ErrNotFound
	}

	fd, err := gh.getFileRecord(fid)
	if err != nil {
		logs.Warn.Println("Download: file not found", fid)
		return nil, nil, err
	}

	// Download from GCS
	ctx := context.Background()
	reader, err := gh.downloadFile(ctx, gh.bucketName, fd.Location)
	if err != nil {
		logs.Warn.Println("Download: failed to download from GCS", fd.Location, err)
		return nil, nil, err
	}

	// Create a ReadSeekCloser wrapper
	readSeekCloser := &gcsReadSeekCloser{
		reader: reader,
		close:  func() error { return nil }, // GCS reader doesn't need explicit close
	}

	return fd, readSeekCloser, nil
}

// Delete deletes files from storage by provided slice of locations.
func (gh *gcshandler) Delete(locations []string) error {
	ctx := context.Background()
	for _, loc := range locations {
		if err := gh.deleteFile(ctx, gh.bucketName, loc); err != nil {
			logs.Warn.Println("gcs: error deleting file", loc, err)
		}
	}
	return nil
}

// GetIdFromUrl converts an attachment URL to a file UID.
func (gh *gcshandler) GetIdFromUrl(url string) types.Uid {
	return media.GetIdFromUrl(url, gh.serveURL)
}

// getFileRecord given file ID reads file record from the database.
func (gh *gcshandler) getFileRecord(fid types.Uid) (*types.FileDef, error) {
	fd, err := store.Files.Get(fid.String())
	if err != nil {
		return nil, err
	}
	if fd == nil {
		return nil, types.ErrNotFound
	}
	return fd, nil
}

// GCS helper methods
func (gh *gcshandler) uploadFileWithKMS(ctx context.Context, bucketName, objectName string, file io.Reader) error {
	kmsKeyName := fmt.Sprintf(
		"projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s",
		gh.projectID,
		gh.locationID,
		gh.keyRingID,
		gh.keyID,
	)

	obj := gh.client.Bucket(bucketName).Object(objectName)
	w := obj.NewWriter(ctx)
	w.KMSKeyName = kmsKeyName
	defer w.Close()

	if _, err := io.Copy(w, file); err != nil {
		return fmt.Errorf("failed to upload file with KMS encryption: %w", err)
	}

	return nil
}

func (gh *gcshandler) downloadFile(ctx context.Context, bucketName, objectName string) (io.Reader, error) {
	bkt := gh.client.Bucket(bucketName)
	obj := bkt.Object(objectName)
	r, err := obj.NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open reader: %w", err)
	}
	return r, nil
}

func (gh *gcshandler) deleteFile(ctx context.Context, bucketName, objectName string) error {
	bkt := gh.client.Bucket(bucketName)
	obj := bkt.Object(objectName)
	if err := obj.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}
	return nil
}

func (gh *gcshandler) getFileSize(ctx context.Context, bucketName, objectName string) (int64, error) {
	objAttrs, err := gh.client.Bucket(bucketName).Object(objectName).Attrs(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get object attrs: %w", err)
	}
	return objAttrs.Size, nil
}

// gcsReadSeekCloser implements media.ReadSeekCloser interface
type gcsReadSeekCloser struct {
	reader io.Reader
	close  func() error
}

func (g *gcsReadSeekCloser) Read(p []byte) (n int, err error) {
	return g.reader.Read(p)
}

func (g *gcsReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	// GCS doesn't support seeking, so we return an error
	return 0, errors.New("seeking not supported for GCS objects")
}

func (g *gcsReadSeekCloser) Close() error {
	return g.close()
}

func etagFromObjectName(objectName string) string {
	// Use object name as ETag since it's unique
	return strings.ToLower(objectName)
}

func init() {
	store.RegisterMediaHandler(handlerName, &gcshandler{})
}
