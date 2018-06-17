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

	// Upload processes request for file upload.
	Upload(fdef *types.FileDef, file io.Reader) (string, error)

	// Download processes request for file download.
	Download(url string) (ReadSeekCloser, error)

	// Delete deletes file from storage.
	Delete(fid string) error
}
