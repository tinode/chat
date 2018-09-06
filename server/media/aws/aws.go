package aws

import (
	"io"
	"log"
	"github.com/tinode/chat/server/store/types"
	"github.com/tinode/chat/server/media"
	"github.com/tinode/chat/server/store"
	"encoding/json"
	"github.com/kataras/iris/core/errors"
	"github.com/google/go-cloud/blob"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/google/go-cloud/blob/s3blob"
	"context"
	"mime"
	"path"
	"strings"
)

const (
	defaultServeURL = "/v0/file/s/"
)

type awsconfig struct {
	AccessKeyId     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	Region          string `json:"region,omitempty"`
	Bucket          string `json:"bucket,omitempty"`
	ServeURL        string `json:"serve_url,omitempty"`
}

func (ac awsconfig) validate() bool {
	return ac.AccessKeyId != "" &&
		ac.SecretAccessKey != "" &&
		ac.Region != "" &&
		ac.Bucket != ""
}

type awshandler struct {
	*blob.Bucket
	conf awsconfig
}

type awsReadSeekCloser struct {
	*blob.Reader
}

func (ars awsReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	return ars.Size(), nil
}

func (ah *awshandler) Init(jsconf string) error {
	var config awsconfig
	if err := json.Unmarshal([]byte(jsconf), &config); err != nil {
		return errors.New("aws handler failed to parse config: " + err.Error())
	}
	if !config.validate() {
		return errors.New("aws handler failed ,missing access_key_id 、secret_access_key、region、bucket")
	}
	if config.ServeURL == "" {
		config.ServeURL = defaultServeURL
	}
	c := &aws.Config{
		Region:      aws.String(config.Region),
		Credentials: credentials.NewStaticCredentials(config.AccessKeyId, config.SecretAccessKey, ""),
	}
	s := session.Must(session.NewSession(c))

	bucket, err := s3blob.OpenBucket(context.Background(), s, config.Bucket)
	if err != nil {
		return errors.New("aws handler failed ,s3blob open bucket " + err.Error())
	}
	ah.conf = config
	ah.Bucket = bucket
	return nil
}

func (ah awshandler) Redirect(url string) string {
	//FIXME go-cloud not support generate link url currently,
	return ""
}

func (ah *awshandler) Upload(fdef *types.FileDef, file io.Reader) (string, error) {
	key := fdef.Uid().String32()
	fdef.Location = key
	writer, err := ah.Bucket.NewWriter(context.Background(), key, &blob.WriterOptions{ContentType: fdef.MimeType})
	if err != nil {
		log.Println("Upload: failed to create writer for aws", fdef.Uid(), err)
		return "", err
	}
	defer writer.Close()
	if err = store.Files.StartUpload(fdef); err != nil {
		ah.Bucket.Delete(context.Background(), key)
		log.Println("fs: failed to create file record", fdef.Id, err)
		return "", err
	}

	size, err := io.Copy(writer, file)
	if err != nil {
		store.Files.FinishUpload(fdef.Id, false, 0)
		ah.Bucket.Delete(context.Background(), key)
		return "", err
	}

	fdef, err = store.Files.FinishUpload(fdef.Id, true, size)
	if err != nil {
		ah.Bucket.Delete(context.Background(), key)
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
func (ah *awshandler) Download(url string) (*types.FileDef, media.ReadSeekCloser, error) {

	fid := ah.GetIdFromUrl(url)
	if fid.IsZero() {
		return nil, nil, types.ErrNotFound
	}
	fd, err := ah.getFileRecord(fid)
	if err != nil {
		log.Println("aws download: file not found", fid)
		return nil, nil, err
	}
	reader, err := ah.NewReader(context.Background(), fd.Uid().String32())

	if err != nil {
		log.Println("aws download: read error ", err.Error())
		return nil, nil, err
	}

	return fd, awsReadSeekCloser{reader}, nil
}

// Delete deletes files from aws by provided slice of locations.
func (ah *awshandler) Delete(locations []string) error {
	for _, loc := range locations {
		err := ah.Bucket.Delete(context.Background(), loc)
		if err != nil {
			log.Println("aws delete ", loc, "err", err.Error())
		}
	}
	log.Println("aws delete ", locations)
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
