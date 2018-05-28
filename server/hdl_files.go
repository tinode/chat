/******************************************************************************
 *
 *  Description :
 *
 *    Handler of large file uploads/downloads.
 *    Default upload handler saves files to the file system at the configured
 *    mount point.
 *
 *    This module cannot handle large volume of data. I's intended only to
 *    show how it's supposed to work.
 *
 *    Use commercial services (Amazon's S3 or Google's/MSFT's equivalents),
 *    or open source Ceph or Minio.
 *
 *****************************************************************************/

package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// largeFileUpload receives files from HTTP(S) and saves them to local file
// system.
func largeFileUpload(wrt http.ResponseWriter, req *http.Request) {
	now := time.Now().UTC().Round(time.Millisecond)

	// Check for API key presence
	enc := json.NewEncoder(wrt)
	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		wrt.WriteHeader(http.StatusForbidden)
		enc.Encode(ErrAPIKeyRequired(now))
		return
	}

	// Check if this is a POST request
	if req.Method != http.MethodPost {
		wrt.WriteHeader(http.StatusMethodNotAllowed)
		enc.Encode(ErrOperationNotAllowed("", "", now))
		return
	}

	// Check authorization: either the token or SID must be present
	var uid types.Uid
	token := req.FormValue("token")
	if token != "" {
		authhdl := store.GetAuthHandler("token")
		if authhdl != nil {
			rec, err := authhdl.Authenticate([]byte(token))
			uid = rec.Uid
		} else {
			log.Println("fileUpload: token is present but token auth handler is not found")
		}
	} else {
		// Find the session, make sure it's appropriately authenticated.
		sess := globals.sessionStore.Get(req.FormValue("sid"))
		if sess != nil {
			uid = sess.uid
		}
	}

	if uid.IsZero() {
		// Not authenticated
		wrt.WriteHeader(http.StatusUnauthorized)
		enc.Encode(ErrAuthRequired("", "", now))
		return
	}

	fname := req.FormValue("filename")
	log.Println("Starting upload", fname)
	file, _, err := req.FormFile("file")
	if err != nil {
		wrt.WriteHeader(http.StatusBadRequest)
		enc.Encode(ErrMalformed("", "", now))
		return
	}

	// FIXME: The following code needs to be replaced in production with calls to S3, etc

	// Generate unique file name.
	filename := store.GetUidString()
	outfile, err := os.Create(filepath.Join(globals.fileUploadLocation, filename))
	if err != nil {
		wrt.WriteHeader(http.StatusInternalServerError)
		enc.Encode(ErrUnknown("", "", now))
		return
	}
	defer outfile.Close()

	_, err = io.Copy(outfile, file)
	if err != nil {
		wrt.WriteHeader(http.StatusInternalServerError)
		enc.Encode(ErrUnknown("", "", now))
		return
	}

	wrt.WriteHeader(http.StatusOK)
	enc.Encode(NoErr("", "", now))
}
