// Pckage fs implements media interface storing media objects in a file system.
package fs

import (
	"io"
	"log"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tinode/chat/server/media"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	defaultServeURL = "/v0/file/s/"
)

type fshandler struct {
	fileUploadLocation string
	serveURL           string
}

func (fh *fshandler) Init(jsconf string) error {
	return nil
}

// Upload processes request for file upload. The file is given as io.Reader.
func (fh *fshandler) Upload(fdef *types.FileDef, file io.Reader) (string, error) {
	// Generate a unique file name and attach it to path. Using base32 to avoid possible
	// file name collisions on Windows.
	// FIXME: create two-three levels of nested directories. Serving from a single directory
	// with tens of thousands of files in it will not perform well.
	fdef.Location = filepath.Join(fh.fileUploadLocation, fdef.Uid().String32())

	outfile, err := os.Create(fdef.Location)
	if err != nil {
		log.Println("Failed to create file", fdef.Location, err)
		return "", err
	}

	if err = store.Files.StartUpload(fdef); err != nil {
		outfile.Close()
		os.Remove(fdef.Location)
		log.Println("Failed to create file record", fdef.Id, err)
		return "", err
	}

	size, err := io.Copy(outfile, file)
	log.Println("Finished upload", fdef.Location)
	outfile.Close()
	if err != nil {
		store.Files.FinishUpload(fdef.Id, false, 0)
		os.Remove(fdef.Location)
		return "", err
	}

	store.Files.FinishUpload(fdef.Id, true, size)

	fname := fdef.Id
	ext, _ := mime.ExtensionsByType(fdef.MimeType)
	if len(ext) > 0 {
		fname += ext[0]
	}

	return fh.serveURL + fname, nil
}

// Download processes request for file download.
// The returned ReadSeekCloser must be closed after use.
func (fh *fshandler) Download(url string) (media.ReadSeekCloser, error) {
	dir, fname := path.Split(path.Clean(url))

	// Check path validity
	if dir != defaultServeURL {
		return nil, types.ErrNotFound
	}

	// Remove extension
	parts := strings.Split(fname, ".")
	fname = parts[0]

	log.Println("Fetching file record for", fname)

	fd, err := store.Files.Get(fname)
	if err != nil {
		return nil, err
	}
	if fd == nil {
		log.Println("File record not found", fname)
		return nil, types.ErrNotFound
	}

	// FIXME: The following code is dependent on the storage method.
	log.Println("Opening file", fd.Location)
	file, err := os.Open(fd.Location)
	if err != nil {
		return nil, err
	}

	return file, nil
}

// Attach notifies the handler that certain file was sent as an attachment to the given message.
func (fshandler) Attach(fid string, topic string, seqid int) error {
	return nil
}

// Delete deletes file from storage
func (fshandler) Delete(fid string) error {
	return nil
}

func init() {
	store.RegisterMediaHandler("fs", &fshandler{})
}
