// Interface which must be implemented by media upload/download handlers.

package media

import (
	"io"

	"github.com/tinode/chat/server/store/types"
)

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type Handler interface {
	// Init initializes the media upload handler.
	Init(jsconf string) error

	// Check if redirect is required.
	// Redirect can be used to serve files from a different external server.
	Redirect(url string) string

	// Upload processes request for file upload.
	Upload(fdef *types.FileDef, file io.Reader) (string, error)

	// Download processes request for file download.
	Download(url string) (*types.FileDef, ReadSeekCloser, error)

	// Delete deletes file from storage.
	Delete(fid types.Uid) error

	// GetIdFromUrl extracts file ID from download URL.
	GetIdFromUrl(url string) types.Uid
}
