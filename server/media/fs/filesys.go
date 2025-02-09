// Package fs implements github.com/tinode/chat/server/media interface by storing media objects in a single
// directory in the file system.
// This module won't perform well with tens of thousand of files because it stores all files in a single directory.
package fs

import (
	"encoding/base32"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/media"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	defaultServeURL     = "/v0/file/s/"
	defaultCacheControl = "max-age=86400"

	handlerName = "fs"
)

type configType struct {
	FileUploadDirectory string   `json:"upload_dir"`
	ServeURL            string   `json:"serve_url"`
	CorsOrigins         []string `json:"cors_origins"`
	CacheControl        string   `json:"cache_control"`
}

type fshandler struct {
	// In case of a cluster fileUploadLocation must be accessible to all cluster members.
	fileUploadLocation string
	serveURL           string
	corsOrigins        []string
	cacheControl       string
}

func (fh *fshandler) Init(jsconf string) error {
	var err error
	var config configType

	if err = json.Unmarshal([]byte(jsconf), &config); err != nil {
		return errors.New("failed to parse config: " + err.Error())
	}

	fh.fileUploadLocation = config.FileUploadDirectory
	if fh.fileUploadLocation == "" {
		return errors.New("missing upload location")
	}

	fh.serveURL = config.ServeURL
	if fh.serveURL == "" {
		fh.serveURL = defaultServeURL
	}

	fh.cacheControl = config.CacheControl
	if fh.cacheControl == "" {
		fh.serveURL = defaultCacheControl
	}

	// Make sure the upload directory exists.
	return os.MkdirAll(fh.fileUploadLocation, 0777)
}

// Headers is used for cache management and serving CORS headers.
func (fh *fshandler) Headers(method string, url *url.URL, headers http.Header, serve bool) (http.Header, int, error) {
	if method == http.MethodGet {

		fid := fh.GetIdFromUrl(url.String())
		if fid.IsZero() {
			return nil, 0, types.ErrNotFound
		}

		fdef, err := fh.getFileRecord(fid)
		if err != nil {
			return nil, 0, err
		}

		if etag := strings.Trim(headers.Get("If-None-Match"), "\""); etag != "" && etag == fdef.ETag {
			return http.Header{
					"Last-Modified": {fdef.UpdatedAt.Format(http.TimeFormat)},
					"ETag":          {`"` + fdef.ETag + `"`},
					"Cache-Control": {fh.cacheControl},
				},
				http.StatusNotModified, nil
		}

		return http.Header{
			"Content-Type":  {fdef.MimeType},
			"Cache-Control": {fh.cacheControl},
			"ETag":          {`"` + fdef.ETag + `"`},
		}, 0, nil
	}

	if method != http.MethodOptions {
		// Not an OPTIONS request. No special handling for all other requests.
		return nil, 0, nil
	}
	header, status := media.CORSHandler(method, headers, fh.corsOrigins, serve)
	return header, status, nil
}

// Upload processes request for file upload. The file is given as io.Reader.
func (fh *fshandler) Upload(fdef *types.FileDef, file io.Reader) (string, int64, error) {
	// FIXME: create two-three levels of nested directories. Serving from a single directory
	// with tens of thousands of files in it will not perform well.

	// Generate a unique file name and attach it to path. Using base32 instead of base64 to avoid possible
	// file name collisions on Windows due to case-insensitive file names there.
	location := filepath.Join(fh.fileUploadLocation, fdef.Uid().String32())

	outfile, err := os.Create(location)
	if err != nil {
		logs.Warn.Println("Upload: failed to create file", fdef.Location, err)
		return "", 0, err
	}

	if err = store.Files.StartUpload(fdef); err != nil {
		outfile.Close()
		os.Remove(location)
		logs.Warn.Println("failed to create file record", fdef.Id, err)
		return "", 0, err
	}

	size, err := io.Copy(outfile, file)
	outfile.Close()
	if err != nil {
		os.Remove(location)
		return "", 0, err
	}

	fname := fdef.Id
	ext, _ := mime.ExtensionsByType(fdef.MimeType)
	if len(ext) > 0 {
		fname += ext[0]
	}

	fdef.Location = location
	// Use file path to create ETag. File paths are unique so will be the ETag.
	fdef.ETag = etagFromPath(fdef.Location)

	return fh.serveURL + fname, size, nil
}

// Download processes request for file download.
// The returned ReadSeekCloser must be closed after use.
func (fh *fshandler) Download(url string) (*types.FileDef, media.ReadSeekCloser, error) {
	fid := fh.GetIdFromUrl(url)
	if fid.IsZero() {
		return nil, nil, types.ErrNotFound
	}

	fd, err := fh.getFileRecord(fid)
	if err != nil {
		logs.Warn.Println("Download: file not found", fid)
		return nil, nil, err
	}

	file, err := os.Open(fd.Location)
	if err != nil {
		if os.IsNotExist(err) {
			// If the file is not found, send 404 instead of the default 500
			err = types.ErrNotFound
		}
		return nil, nil, err
	}

	return fd, file, nil
}

// Delete deletes files from storage by provided slice of locations.
func (fh *fshandler) Delete(locations []string) error {
	for _, loc := range locations {
		if err, _ := os.Remove(loc).(*os.PathError); err != nil {
			if err != os.ErrNotExist {
				logs.Warn.Println("fs: error deleting file", loc, err)
			}
		}
	}
	return nil
}

// GetIdFromUrl converts an attahment URL to a file UID.
func (fh *fshandler) GetIdFromUrl(url string) types.Uid {
	return media.GetIdFromUrl(url, fh.serveURL)
}

// getFileRecord given file ID reads file record from the database.
func (fh *fshandler) getFileRecord(fid types.Uid) (*types.FileDef, error) {
	fd, err := store.Files.Get(fid.String())
	if err != nil {
		return nil, err
	}
	if fd == nil {
		return nil, types.ErrNotFound
	}
	return fd, nil
}

func etagFromPath(path string) string {
	hasher := fnv.New128()
	hasher.Write([]byte(path))
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString(hasher.Sum(make([]byte, 0, hasher.Size()))))
}

func init() {
	store.RegisterMediaHandler(handlerName, &fshandler{})
}
