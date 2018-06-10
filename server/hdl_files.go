/******************************************************************************
 *
 *  Description :
 *
 *    Handler of large file uploads/downloads.
 *    Default upload handler saves files to the file system at the configured
 *    mount point.
 *
 *    This module cannot handle large volume of data. It's intended only to
 *    show how it's supposed to work.
 *
 *    Use commercial services (Amazon S3 or Google's/MSFT's equivalents),
 *    or open source like Ceph or Minio.
 *
 *****************************************************************************/

package main

import (
	"encoding/json"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

func largeFileServe(wrt http.ResponseWriter, req *http.Request) {
	log.Println("Request to download file", req.URL.Path)

	now := time.Now().UTC().Round(time.Millisecond)
	enc := json.NewEncoder(wrt)

	writeHttpResponse := func(msg *ServerComMessage) {
		if msg == nil {
			msg = ErrUnknown("", "", now)
		}
		// Gorilla CompressHandler requires Content-Type to be set.
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.WriteHeader(msg.Ctrl.Code)
		enc.Encode(msg)
	}

	// Check for API key presence
	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		writeHttpResponse(ErrAPIKeyRequired(now))
		return
	}

	// Check authorization: either the token or SID must be present
	uid, err := authHttpRequest(req)
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, nil))
		return
	}
	if uid.IsZero() {
		// Not authenticated
		writeHttpResponse(ErrAuthRequired("", "", now))
		return
	}

	dir, fname := path.Split(path.Clean(req.URL.Path))

	// Check path validity
	if dir != "/v0/file/s/" {
		writeHttpResponse(ErrNotFound("", "", now))
		return
	}

	// Remove extension
	parts := strings.Split(fname, ".")
	fname = parts[0]

	log.Println("Fetching file record for", fname)

	fd, err := store.Files.Get(fname)
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, nil))
		return
	}
	if fd == nil {
		log.Println("File record not found", fname)
		writeHttpResponse(ErrNotFound("", "", now))
		return
	}

	// FIXME: The following code is dependent on the storage method.
	log.Println("Opening file", fd.Location)
	file, err := os.Open(fd.Location)
	if err != nil {
		writeHttpResponse(nil)
		return
	}
	defer file.Close()

	wrt.Header().Set("Content-Type", fd.MimeType)
	http.ServeContent(wrt, req, "", fd.UpdatedAt, file)
}

// largeFileUpload receives files from client over HTTP(S) and saves them to local file
// system.
func largeFileUpload(wrt http.ResponseWriter, req *http.Request) {
	now := time.Now().UTC().Round(time.Millisecond)
	enc := json.NewEncoder(wrt)

	writeHttpResponse := func(msg *ServerComMessage) {
		if msg == nil {
			msg = ErrUnknown("", "", now)
		}
		// Gorilla CompressHandler requires Content-Type to be set.
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.WriteHeader(msg.Ctrl.Code)
		enc.Encode(msg)
	}

	// Check if this is a POST request
	if req.Method != http.MethodPost {
		writeHttpResponse(ErrOperationNotAllowed("", "", now))
		return
	}

	// Limit the size of the uploaded file.
	req.Body = http.MaxBytesReader(wrt, req.Body, globals.maxUploadSize)

	// Check for API key presence
	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		writeHttpResponse(ErrAPIKeyRequired(now))
		return
	}
	// Check authorization: either the token or SID must be present
	uid, err := authHttpRequest(req)
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, nil))
		return
	}
	if uid.IsZero() {
		// Not authenticated
		writeHttpResponse(ErrAuthRequired("", "", now))
		return
	}

	log.Println("Starting upload", req.FormValue("name"))
	file, _, err := req.FormFile("file")
	if err != nil {
		log.Println("Error reading file", err)
		if strings.Contains(err.Error(), "request body too large") {
			log.Println("Uploaded file is too large", err)
			writeHttpResponse(ErrTooLarge("", "", now))
			if strings.Contains(err.Error(), "unexpected EOF") {
				log.Println("Upload interrupted by client", err)
				writeHttpResponse(ErrMalformed("", "", now))
			} else {
				log.Println("Upload error", err)
				writeHttpResponse(ErrMalformed("", "", now))
			}
			return
		}
	}
	fdef := types.FileDef{}
	fdef.Id = store.GetUidString()
	fdef.InitTimes()
	fdef.User = uid.String()

	buff := make([]byte, 512)
	if _, err = file.Read(buff); err != nil {
		log.Println("Failed to detect mime type", err)
		writeHttpResponse(nil)
		return
	}

	fdef.MimeType = http.DetectContentType(buff)
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		log.Println("Failed to reset request buffer", err)
		writeHttpResponse(nil)
		return
	}

	// FIXME: The following code needs to be replaced in production with calls to S3,
	// GCS, ABS, Minio, Ceph, etc.

	// Generate a unique file name and attach it to path.
	// FIXME: create two-three levels of nested directories. Serving from a directory with
	// tens of thousands of files in it will not perform well.
	fdef.Location = filepath.Join(globals.fileUploadLocation, fdef.Id)

	outfile, err := os.Create(fdef.Location)
	if err != nil {
		log.Println("Failed to create file", fdef.Location, err)
		writeHttpResponse(nil)
		return
	}

	if err = store.Files.StartUpload(&fdef); err != nil {
		outfile.Close()
		os.Remove(fdef.Location)
		log.Println("Failed to create file record", fdef.Id, err)
		writeHttpResponse(decodeStoreError(err, "", "", now, nil))
		return
	}

	_, err = io.Copy(outfile, file)
	log.Println("Finished upload", fdef.Location)
	outfile.Close()
	if err != nil {
		store.Files.FinishUpload(fdef.Id, false)
		os.Remove(fdef.Location)
		writeHttpResponse(nil)
		return
	}

	store.Files.FinishUpload(fdef.Id, true)

	fname := fdef.Id
	ext, _ := mime.ExtensionsByType(fdef.MimeType)
	if len(ext) > 0 {
		fname += ext[0]
	}
	resp := NoErr("", "", now)
	resp.Ctrl.Params = map[string]string{"url": "/v0/file/s/" + fname}
	writeHttpResponse(resp)
}
