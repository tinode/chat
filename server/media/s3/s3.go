// Package s3 implements media interface by storing media objects in Amazon S3 bucket.
package s3

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/media"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	defaultServeURL     = "/v0/file/s/"
	defaultCacheControl = "no-cache, must-revalidate"

	handlerName = "s3"
	// Presign GET URLs for this number of seconds.
	defaultPresignDuration = 120
)

type awsconfig struct {
	AccessKeyId     string   `json:"access_key_id"`
	SecretAccessKey string   `json:"secret_access_key"`
	Region          string   `json:"region"`
	DisableSSL      bool     `json:"disable_ssl"`
	ForcePathStyle  bool     `json:"force_path_style"`
	Endpoint        string   `json:"endpoint"`
	BucketName      string   `json:"bucket"`
	CorsOrigins     []string `json:"cors_origins"`
	ServeURL        string   `json:"serve_url"`
	PresignTTL      int      `json:"presign_ttl"`
	CacheControl    string   `json:"cache_control"`
}

type awshandler struct {
	svc         *s3.Client
	presign     *s3.PresignClient
	conf        awsconfig
	corsOrigins []media.AllowedOrigin
}

// readerCounter is a byte counter for bytes read through the io.Reader
type readerCounter struct {
	io.Reader
	count  int64
	reader io.Reader
}

// Read reads the bytes and records the number of read bytes.
func (rc *readerCounter) Read(buf []byte) (int, error) {
	n, err := rc.reader.Read(buf)
	atomic.AddInt64(&rc.count, int64(n))
	return n, err
}

// Init initializes the media handler.
func (ah *awshandler) Init(jsconf string) error {
	var err error
	if err = json.Unmarshal([]byte(jsconf), &ah.conf); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	if ah.conf.AccessKeyId == "" {
		return errors.New("missing Access Key ID")
	}
	if ah.conf.SecretAccessKey == "" {
		return errors.New("missing Secret Access Key")
	}
	if ah.conf.Region == "" {
		return errors.New("missing Region")
	}
	if ah.conf.BucketName == "" {
		return errors.New("missing Bucket")
	}
	if ah.conf.PresignTTL <= 0 {
		ah.conf.PresignTTL = defaultPresignDuration
	}
	if ah.conf.CacheControl == "" {
		ah.conf.CacheControl = defaultCacheControl
	}
	if ah.conf.ServeURL == "" {
		ah.conf.ServeURL = defaultServeURL
	}
	ah.corsOrigins, err = media.ParseCORSAllow(ah.conf.CorsOrigins)
	if err != nil {
		return errors.New("failed to parse CORS allowed origins: " + err.Error())
	}

	cfgOpts := []func(*config.LoadOptions) error{
		config.WithRegion(ah.conf.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			ah.conf.AccessKeyId,
			ah.conf.SecretAccessKey,
			"",
		)),
	}

	var cfg aws.Config
	if cfg, err = config.LoadDefaultConfig(context.Background(), cfgOpts...); err != nil {
		return err
	}

	// Create S3 service client
	clientOpts := []func(*s3.Options){
		func(o *s3.Options) {
			o.UsePathStyle = ah.conf.ForcePathStyle
		},
	}
	if ah.conf.Endpoint != "" {
		endpoint := ah.conf.Endpoint
		if !strings.Contains(endpoint, "://") {
			if ah.conf.DisableSSL {
				endpoint = "http://" + endpoint
			} else {
				endpoint = "https://" + endpoint
			}
		}
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}
	ah.svc = s3.NewFromConfig(cfg, clientOpts...)
	ah.presign = s3.NewPresignClient(ah.svc)

	// Check if bucket already exists.
	_, err = ah.svc.HeadBucket(context.Background(), &s3.HeadBucketInput{Bucket: aws.String(ah.conf.BucketName)})
	if err == nil {
		// Bucket exists
		return nil
	}

	if !isAPIError(err, "NoSuchBucket", "NotFound") {
		// Hard error.
		return err
	}

	// Bucket does not exist. Create one.
	_, err = ah.svc.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(ah.conf.BucketName)})
	if err != nil {
		if isAPIError(err, "BucketAlreadyExists", "BucketAlreadyOwnedByYou", "OperationAborted") {
			// Check if someone has already created a bucket (possible in a cluster).
			err = nil
		}
	} else {
		// This is a new bucket.

		// The following serves two purposes:
		// 1. Setup CORS policy to be able to serve media directly from S3.
		// 2. Verify that the bucket is accessible to the current user.
		origins := ah.conf.CorsOrigins
		if len(origins) == 0 {
			origins = append(origins, "*")
		}
		_, err = ah.svc.PutBucketCors(context.Background(), &s3.PutBucketCorsInput{
			Bucket: aws.String(ah.conf.BucketName),
			CORSConfiguration: &s3types.CORSConfiguration{
				CORSRules: []s3types.CORSRule{{
					AllowedMethods: []string{http.MethodGet, http.MethodHead},
					AllowedOrigins: origins,
					AllowedHeaders: []string{"*"},
				}},
			},
		})
	}
	return err
}

// Headers adds CORS headers and redirects GET and HEAD requests to the AWS server.
func (ah *awshandler) Headers(method string, url *url.URL, headers http.Header, serve bool) (http.Header, int, error) {
	// Add CORS headers, if necessary.
	headers, status := media.CORSHandler(method, headers, ah.corsOrigins, serve)
	if status != 0 || method == http.MethodPost || method == http.MethodPut {
		return headers, status, nil
	}

	fid := ah.GetIdFromUrl(url.String())
	if fid.IsZero() {
		return nil, 0, types.ErrNotFound
	}

	fdef, err := ah.getFileRecord(fid)
	if err != nil {
		return nil, 0, err
	}

	if fdef.ETag != "" && headers.Get("If-None-Match") == `"`+fdef.ETag+`"` {
		return http.Header{
				"ETag":          {`"` + fdef.ETag + `"`},
				"Cache-Control": {ah.conf.CacheControl},
			},
			http.StatusNotModified, nil
	}

	ctx := context.Background()
	var redirURL string
	switch method {
	case http.MethodGet:
		// If the query parameter "asatt" is set to a true, set Content-Disposition to attachment.
		// This will cause browsers to download the file rather than attempt to display it.
		// This closes an XSS vulnerability when users upload HTML files.
		var contentDisposition *string
		if isAttachment, _ := strconv.ParseBool(url.Query().Get("asatt")); isAttachment {
			contentDisposition = aws.String("attachment")
		}
		presigned, err := ah.presign.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket:                     aws.String(ah.conf.BucketName),
			Key:                        aws.String(fid.String32()),
			ResponseCacheControl:       aws.String(ah.conf.CacheControl),
			ResponseContentType:        aws.String(fdef.MimeType),
			ResponseContentDisposition: contentDisposition,
		}, func(opts *s3.PresignOptions) {
			opts.Expires = time.Second * time.Duration(ah.conf.PresignTTL)
		})
		if err != nil {
			return nil, 0, err
		}
		redirURL = presigned.URL
	case http.MethodHead:
		presigned, err := ah.presign.PresignHeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(ah.conf.BucketName),
			Key:    aws.String(fid.String32()),
		}, func(opts *s3.PresignOptions) {
			opts.Expires = time.Second * time.Duration(ah.conf.PresignTTL)
		})
		if err != nil {
			return nil, 0, err
		}
		redirURL = presigned.URL
	}

	if redirURL != "" {
		// Return presigned URL with 308 Permanent redirect. Let the client cache the response.
		// The original URL will stop working after a short period of time to prevent use of Tinode
		// as a free file server.
		return http.Header{
				"Location":      {redirURL},
				"ETag":          {`"` + fdef.ETag + `"`},
				"Content-Type":  {"application/json; charset=utf-8"},
				"Cache-Control": {ah.conf.CacheControl},
			},
			http.StatusPermanentRedirect, nil
	}
	return nil, 0, nil
}

// Upload processes request for a file upload. The file is given as io.Reader.
func (ah *awshandler) Upload(fdef *types.FileDef, file io.Reader) (string, int64, error) {
	var err error

	// Using String32 just for consistency with the file handler.
	key := fdef.Uid().String32()

	tmClient := transfermanager.New(ah.svc)

	if err = store.Files.StartUpload(fdef); err != nil {
		logs.Warn.Println("failed to create file record", fdef.Id, err)
		return "", 0, err
	}

	rc := readerCounter{reader: file}
	result, err := tmClient.UploadObject(context.Background(), &transfermanager.UploadObjectInput{
		CacheControl: aws.String(ah.conf.CacheControl),
		Bucket:       aws.String(ah.conf.BucketName),
		Key:          aws.String(key),
		Body:         &rc,
	})

	if err != nil {
		return "", 0, err
	}

	fname := fdef.Id
	ext, _ := mime.ExtensionsByType(fdef.MimeType)
	if len(ext) > 0 {
		fname += ext[0]
	}

	fdef.Location = key
	if result.ETag != nil {
		fdef.ETag = strings.Trim(*result.ETag, "\"")
	}
	return ah.conf.ServeURL + fname, rc.count, nil
}

// Download processes request for file download.
// The returned ReadSeekCloser must be closed after use.
func (ah *awshandler) Download(url string) (*types.FileDef, media.ReadSeekCloser, error) {
	return nil, nil, types.ErrUnsupported
}

// Delete deletes files from aws by provided slice of locations.
func (ah *awshandler) Delete(locations []string) error {
	ctx := context.Background()
	for i := 0; i < len(locations); i += 1000 {
		end := i + 1000
		if end > len(locations) {
			end = len(locations)
		}

		objects := make([]s3types.ObjectIdentifier, end-i)
		for j, key := range locations[i:end] {
			objects[j] = s3types.ObjectIdentifier{Key: aws.String(key)}
		}

		_, err := ah.svc.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(ah.conf.BucketName),
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func isAPIError(err error, codes ...string) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	for _, code := range codes {
		if apiErr.ErrorCode() == code {
			return true
		}
	}
	return false
}

// GetIdFromUrl converts an attahment URL to a file UID.
func (ah *awshandler) GetIdFromUrl(url string) types.Uid {
	return media.GetIdFromUrl(url, ah.conf.ServeURL)
}

// getFileRecord given file ID reads file record from the database.
func (ah *awshandler) getFileRecord(fid types.Uid) (*types.FileDef, error) {
	fd, err := store.Files.Get(fid.String())
	if err != nil {
		return nil, err
	}
	if fd == nil {
		return nil, types.ErrNotFound
	}
	return fd, nil
}

func init() {
	store.RegisterMediaHandler(handlerName, &awshandler{})
}
