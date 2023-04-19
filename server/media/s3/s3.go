// Package s3 implements media interface by storing media objects in Amazon S3 bucket.
package s3

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/media"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	defaultServeURL = "/v0/file/s/"
	handlerName     = "s3"
	// Presign GET URLs for this number of seconds.
	presignDuration = 120
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
}

type awshandler struct {
	svc  *s3.S3
	conf awsconfig
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

	if ah.conf.ServeURL == "" {
		ah.conf.ServeURL = defaultServeURL
	}

	var sess *session.Session
	if sess, err = session.NewSession(&aws.Config{
		Region:           aws.String(ah.conf.Region),
		DisableSSL:       aws.Bool(ah.conf.DisableSSL),
		S3ForcePathStyle: aws.Bool(ah.conf.ForcePathStyle),
		Endpoint:         aws.String(ah.conf.Endpoint),
		Credentials:      credentials.NewStaticCredentials(ah.conf.AccessKeyId, ah.conf.SecretAccessKey, ""),
	}); err != nil {
		return err
	}

	// Create S3 service client
	ah.svc = s3.New(sess)

	// Check if bucket already exists.
	_, err = ah.svc.HeadBucket(&s3.HeadBucketInput{Bucket: aws.String(ah.conf.BucketName)})
	if err == nil {
		// Bucket exists
		return nil
	}

	if aerr, ok := err.(awserr.Error); !ok || aerr.Code() != s3.ErrCodeNoSuchBucket {
		// Hard error.
		return err
	}

	// Bucket does not exist. Create one.
	_, err = ah.svc.CreateBucket(&s3.CreateBucketInput{Bucket: aws.String(ah.conf.BucketName)})
	if err != nil {
		// Check if someone has already created a bucket (possible in a cluster).
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == s3.ErrCodeBucketAlreadyExists ||
				aerr.Code() == s3.ErrCodeBucketAlreadyOwnedByYou ||
				// Someone is already creating this bucket:
				// OperationAborted: A conflicting conditional operation is currently in progress against this resource.
				aerr.Code() == "OperationAborted" {
				// Clear benign error
				err = nil
			}
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
		_, err = ah.svc.PutBucketCors(&s3.PutBucketCorsInput{
			Bucket: aws.String(ah.conf.BucketName),
			CORSConfiguration: &s3.CORSConfiguration{
				CORSRules: []*s3.CORSRule{{
					AllowedMethods: aws.StringSlice([]string{http.MethodGet, http.MethodHead}),
					AllowedOrigins: aws.StringSlice(origins),
					AllowedHeaders: aws.StringSlice([]string{"*"}),
				}},
			},
		})
	}
	return err
}

// Headers redirects GET, HEAD requests to the AWS server.
func (ah *awshandler) Headers(req *http.Request, serve bool) (http.Header, int, error) {
	if req.Method == http.MethodPut || req.Method == http.MethodPost {
		return nil, 0, nil
	}

	if headers, status := media.CORSHandler(req, ah.conf.CorsOrigins, serve); status != 0 {
		return headers, status, nil
	}

	fid := ah.GetIdFromUrl(req.URL.String())
	if fid.IsZero() {
		return nil, 0, types.ErrNotFound
	}

	fd, err := ah.getFileRecord(fid)
	if err != nil {
		return nil, 0, err
	}

	var awsReq *request.Request
	if req.Method == http.MethodGet {
		var contentDisposition *string
		if isAttachment, _ := strconv.ParseBool(req.URL.Query().Get("asatt")); isAttachment {
			contentDisposition = aws.String("attachment")
		}
		awsReq, _ = ah.svc.GetObjectRequest(&s3.GetObjectInput{
			Bucket:                     aws.String(ah.conf.BucketName),
			Key:                        aws.String(fid.String32()),
			ResponseContentType:        aws.String(fd.MimeType),
			ResponseContentDisposition: contentDisposition,
		})
	} else if req.Method == http.MethodHead {
		awsReq, _ = ah.svc.HeadObjectRequest(&s3.HeadObjectInput{
			Bucket: aws.String(ah.conf.BucketName),
			Key:    aws.String(fid.String32()),
		})
	}

	if awsReq != nil {
		// Return presigned URL. The URL will stop working after a short period of time to prevent use of Tinode
		// as a free file server.
		url, err := awsReq.Presign(time.Second * presignDuration)
		headers := map[string][]string{
			"Location":      {url},
			"Content-Type":  {"application/json; charset=utf-8"},
			"Cache-Control": {"no-cache, no-store, must-revalidate"},
		}
		return headers, http.StatusTemporaryRedirect, err
	}
	return nil, 0, nil
}

// Upload processes request for a file upload. The file is given as io.Reader.
func (ah *awshandler) Upload(fdef *types.FileDef, file io.ReadSeeker) (string, int64, error) {
	var err error

	// Using String32 just for consistency with the file handler.
	key := fdef.Uid().String32()
	fdef.Location = key

	uploader := s3manager.NewUploaderWithClient(ah.svc)

	if err = store.Files.StartUpload(fdef); err != nil {
		logs.Warn.Println("failed to create file record", fdef.Id, err)
		return "", 0, err
	}

	rc := readerCounter{reader: file}
	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(ah.conf.BucketName),
		Key:    aws.String(key),
		Body:   &rc,
	})

	if err != nil {
		return "", 0, err
	}

	fname := fdef.Id
	ext, _ := mime.ExtensionsByType(fdef.MimeType)
	if len(ext) > 0 {
		fname += ext[0]
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
	toDelete := make([]s3manager.BatchDeleteObject, len(locations))
	for i, key := range locations {
		toDelete[i] = s3manager.BatchDeleteObject{
			Object: &s3.DeleteObjectInput{
				Key:    aws.String(key),
				Bucket: aws.String(ah.conf.BucketName),
			}}
	}
	batcher := s3manager.NewBatchDeleteWithClient(ah.svc)
	return batcher.Delete(aws.BackgroundContext(), &s3manager.DeleteObjectsIterator{
		Objects: toDelete,
	})
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
