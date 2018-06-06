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
 *    Use commercial services (Amazon S3 or Google's/MSFT's equivalents),
 *    or open source like Ceph or Minio.
 *
 *****************************************************************************/

package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

// largeFileUpload receives files from HTTP(S) and saves them to local file
// system.
func largeFileUpload(wrt http.ResponseWriter, req *http.Request) {
	now := time.Now().UTC().Round(time.Millisecond)
	enc := json.NewEncoder(wrt)

	writeResponse := func(msg *ServerComMessage) {
		if msg == nil {
			msg = ErrUnknown("", "", now)
		}
		wrt.WriteHeader(msg.Ctrl.Code)
		enc.Encode(msg)
	}

	// Check if this is a POST request
	if req.Method != http.MethodPost {
		writeResponse(ErrOperationNotAllowed("", "", now))
		return
	}

	// Limit the size of the uploaded file.
	req.Body = http.MaxBytesReader(wrt, req.Body, globals.maxUploadSize)

	// Check for API key presence
	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		writeResponse(ErrAPIKeyRequired(now))
		return
	}

	// Check authorization: either the token or SID must be present
	var uid types.Uid
	if authMethod, secret := getHttpAuth(req); authMethod != "" {
		decodedSecret := make([]byte, base64.StdEncoding.DecodedLen(len(secret)))
		if _, err := base64.StdEncoding.Decode(decodedSecret, []byte(secret)); err != nil {
			writeResponse(ErrMalformed("", "", now))
			return
		}
		authhdl := store.GetAuthHandler(authMethod)
		if authhdl != nil {
			if rec, err := authhdl.Authenticate(decodedSecret); err == nil {
				uid = rec.Uid
			} else {
				writeResponse(decodeStoreError(err, "", "", now, nil))
				return
			}
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
		writeResponse(ErrAuthRequired("", "", now))
		return
	}

	log.Println("Starting upload", req.FormValue("name"))
	file, _, err := req.FormFile("file")
	if err != nil {
		log.Println("Error reading file", err)
		if strings.Contains(err.Error(), "request body too large") {
			writeResponse(ErrTooLarge("", "", now))
		} else {
			writeResponse(ErrMalformed("", "", now))
		}
		return
	}

	fdef := types.FileDef{}
	fdef.Id = store.GetUidString()
	fdef.InitTimes()
	fdef.Name = req.FormValue("name")

	// FIXME: The following code needs to be replaced in production with calls to S3,
	// GCS, ABS, Minio, Ceph, etc.

	// Generate a unique file name and attach it to path.
	// FIXME: create two-three levels of nested directories. Serving from a directory with
	// thousands of files in it will not perform well.
	fdef.Location = filepath.Join(globals.fileUploadLocation, fdef.Id)
	outfile, err := os.Create(fdef.Location)
	if err != nil {
		log.Println("Failed to create file", fdef.Location, err)
		writeResponse(nil)
		return
	}
	defer outfile.Close()

	buff := make([]byte, 512)
	if _, err = file.Read(buff); err != nil {
		log.Println("Failed to detect mime type", err)
		writeResponse(nil)
		return
	}
	fdef.MimeType = http.DetectContentType(buff)

	if _, err = file.Seek(0, io.SeekStart); err != nil {
		log.Println("Failed to reset request buffer", err)
		writeResponse(nil)
		return
	}

	store.Files.StartUpload(uid, &fdef)

	_, err = io.Copy(outfile, file)
	log.Println("Finished upload", fdef.Location)
	if err != nil {
		store.Files.FinishUpload(uid, fdef.Id, false)
		writeResponse(nil)
		return
	}

	store.Files.FinishUpload(uid, fdef.Id, true)

	resp := NoErr("", "", now)
	resp.Ctrl.Params = map[string]string{"url": fdef.Id}
	writeResponse(resp)
}
