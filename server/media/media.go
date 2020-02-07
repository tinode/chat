// Interface which must be implemented by media upload/download handlers.
package media

import (
	"io"
	"path"
	"strings"

	"github.com/tinode/chat/server/store/types"
)

// ReadSeekCloser must be implemented by the media being downloaded.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// Handler is an interface which must be implemented by media handlers (uploaders-downloaders).
type Handler interface {
	// Init initializes the media upload handler.
	Init(jsconf string) error

	// Check if redirect is required.
	// Redirect can be used to serve files from a different external server.
	Redirect(method, url string) (string, error)

	// Upload processes request for file upload.
	Upload(fdef *types.FileDef, file io.ReadSeeker) (string, error)

	// Download processes request for file download.
	Download(url string) (*types.FileDef, ReadSeekCloser, error)

	// Delete deletes file from storage.
	Delete(locations []string) error

	// GetIdFromUrl extracts file ID from download URL.
	GetIdFromUrl(url string) types.Uid
}

// GetIdFromUrl is a helper method for extracting file ID from a URL.
func GetIdFromUrl(url string, serveUrl string) types.Uid {
	dir, fname := path.Split(path.Clean(url))

	if dir != "" && dir != serveUrl {
		return types.ZeroUid
	}

	return types.ParseUid(strings.Split(fname, ".")[0])
}
