package s3

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"path"
	"strings"
	"sync/atomic"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	defaultServeURL = "/v0/file/s/"
)

type awsconfig struct {
	AccessKeyId     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	Region          string `json:"region,omitempty"`
	BucketName      string `json:"bucket,omitempty"`
	ServeURL        string `json:"serve_url,omitempty"`
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

func (rc *readerCounter) Read(buf []byte) (int, error) {
	n, err := rc.reader.Read(buf)
	atomic.AddInt64(&rc.count, int64(n))
	return n, err
}

func (ah *awshandler) Init(jsconf string) error {
	var err error
	if err = json.Unmarshal([]byte(jsconf), &ah.conf); err != nil {
		return errors.New("aws handler failed to parse config: " + err.Error())
	}

	if ah.conf.AccessKeyId != "" {
		return errors.New("aws handler missing Access Key ID")
	}
	if ah.conf.SecretAccessKey != "" {
		return errors.New("aws handler missing Secret Access Key")
	}
	if ah.conf.Region != "" {
		return errors.New("aws handler missing Region")
	}
	if ah.conf.BucketName != "" {
		return errors.New("aws handler missing Bucket")
	}

	if ah.conf.ServeURL == "" {
		ah.conf.ServeURL = defaultServeURL
	}

	var sess *session.Session
	if sess, err = session.NewSession(&aws.Config{
		Region:      aws.String(ah.conf.Region),
		Credentials: credentials.NewStaticCredentials(ah.conf.AccessKeyId, ah.conf.SecretAccessKey, ""),
	}); err != nil {
		return err
	}

	// Create S3 service client
	ah.svc = s3.New(sess)

	// Check if the bucket exists, create one if not.
	_, err = ah.svc.CreateBucket(&s3.CreateBucketInput{Bucket: aws.String(ah.conf.BucketName)})
	if err != nil {
		if aerr, ok := err.(awserr.Error); !ok || aerr.Code() != s3.ErrCodeBucketAlreadyExists {
			return err
		}
	}

	// Get bucket ACL to make sure it's accessible to the current user.
	if _, err = ah.svc.GetBucketAcl(&s3.GetBucketAclInput{Bucket: aws.String(ah.conf.BucketName)}); err != nil {
		return err
	}

	return nil
}

// Redirect is used when one wants to serve files from a different external server.
func (ah awshandler) Redirect(url string) string {
	// Not supported.
	return ""
}

// Upload processes request for a file upload. The file is given as io.Reader.
func (ah *awshandler) Upload(fdef *types.FileDef, file io.ReadSeeker) (string, error) {
	var err error

	key := fdef.Uid().String32()
	fdef.Location = key

	uploader := s3manager.NewUploaderWithClient(ah.svc)

	if err = store.Files.StartUpload(fdef); err != nil {
		log.Println("failed to create file record", fdef.Id, err)
		return "", err
	}

	rc := readerCounter{reader: file}
	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(ah.conf.BucketName),
		Key:    aws.String(key),
		Body:   &rc,
	})

	if err != nil {
		store.Files.FinishUpload(fdef.Id, false, 0)
		return "", err
	}

	fdef, err = store.Files.FinishUpload(fdef.Id, true, rc.count)
	if err != nil {
		// Best effort. Error ignored.
		ah.svc.DeleteObject(&s3.DeleteObjectInput{
			Bucket: aws.String(ah.conf.BucketName),
			Key:    aws.String(key),
		})
		return "", err
	}

	fname := fdef.Id
	ext, _ := mime.ExtensionsByType(fdef.MimeType)
	if len(ext) > 0 {
		fname += ext[0]
	}

	log.Println("aws upload success ", fname, "key", key, "id", fdef.Id)

	return ah.conf.ServeURL + fname, nil
}

// Download processes request for file download.
// The returned ReadSeekCloser must be closed after use.
func (ah *awshandler) Download(url string) (*types.FileDef, io.ReadCloser, error) {

	fid := ah.GetIdFromUrl(url)
	if fid.IsZero() {
		return nil, nil, types.ErrNotFound
	}
	fd, err := ah.getFileRecord(fid)
	if err != nil {
		log.Println("aws download: file not found", fid)
		return nil, nil, err
	}

	result, err := ah.svc.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(ah.conf.BucketName),
		Key:    aws.String(fd.Uid().String32()),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == s3.ErrCodeNoSuchKey {
			err = types.ErrNotFound
		}

		log.Println("aws download: read error ", err.Error())
		return nil, nil, err
	}

	return fd, result.Body, nil
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
	if err := batcher.Delete(aws.BackgroundContext(), &s3manager.DeleteObjectsIterator{
		Objects: toDelete,
	}); err != nil {
		return err
	}

	return nil
}

func (ah *awshandler) GetIdFromUrl(url string) types.Uid {
	dir, fname := path.Split(path.Clean(url))

	if dir != ah.conf.ServeURL {
		return types.ZeroUid
	}

	return types.ParseUid(strings.Split(fname, ".")[0])
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
	store.RegisterMediaHandler("aws", &awshandler{})
}
