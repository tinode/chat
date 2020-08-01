/******************************************************************************
 *
 *  Description :
 *
 *    Handler of large file uploads/downloads. Validates request first then calls
 *    a handler.
 *
 *****************************************************************************/

package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

func largeFileServe(wrt http.ResponseWriter, req *http.Request) {
	now := types.TimeNow()
	enc := json.NewEncoder(wrt)
	mh := store.GetMediaHandler()
	statsInc("FileDownloadsTotal", 1)

	writeHttpResponse := func(msg *ServerComMessage, err error) {
		// Gorilla CompressHandler requires Content-Type to be set.
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.WriteHeader(msg.Ctrl.Code)
		enc.Encode(msg)
		if err != nil {
			log.Println("media serve", err)
		}
	}

	// Check for API key presence
	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		writeHttpResponse(ErrAPIKeyRequired(now), nil)
		return
	}

	// Check authorization: either auth information or SID must be present
	uid, challenge, err := authHttpRequest(req)
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, now, nil), err)
		return
	}
	if challenge != nil {
		writeHttpResponse(InfoChallenge("", now, challenge), nil)
		return
	}
	if uid.IsZero() {
		// Not authenticated
		writeHttpResponse(ErrAuthRequired("", "", now), nil)
		return
	}

	// Check if media handler requests redirection to another service.
	if redirTo, err := mh.Redirect(req.Method, req.URL.String()); redirTo != "" {
		wrt.Header().Set("Location", redirTo)
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		wrt.WriteHeader(http.StatusTemporaryRedirect)
		enc.Encode(InfoFound("", "", now))
		log.Println("media serve redirected", redirTo)
		return
	} else if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, now, nil), err)
		return
	}

	fd, rsc, err := mh.Download(req.URL.String())
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, now, nil), err)
		return
	}

	defer rsc.Close()

	wrt.Header().Set("Content-Type", fd.MimeType)
	wrt.Header().Set("Content-Disposition", "attachment")
	http.ServeContent(wrt, req, "", fd.UpdatedAt, rsc)

	log.Println("media served OK")
}

// largeFileUpload receives files from client over HTTP(S) and saves them to local file
// system.
func largeFileUpload(wrt http.ResponseWriter, req *http.Request) {
	log.Println("Upload request", req.RequestURI)

	now := types.TimeNow()
	enc := json.NewEncoder(wrt)
	mh := store.GetMediaHandler()
	statsInc("FileUploadsTotal", 1)

	writeHttpResponse := func(msg *ServerComMessage, err error) {
		// Gorilla CompressHandler requires Content-Type to be set.
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.WriteHeader(msg.Ctrl.Code)
		enc.Encode(msg)

		log.Println("media upload:", msg.Ctrl.Code, msg.Ctrl.Text, "/", err)
	}

	// Check if this is a POST or a PUT request.
	if req.Method != http.MethodPost && req.Method != http.MethodPut {
		writeHttpResponse(ErrOperationNotAllowed("", "", now), errors.New("method '"+req.Method+"' not allowed"))
		return
	}

	if globals.maxFileUploadSize > 0 {
		// Enforce maximum upload size.
		req.Body = http.MaxBytesReader(wrt, req.Body, globals.maxFileUploadSize)
	}

	// Check for API key presence
	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		writeHttpResponse(ErrAPIKeyRequired(now), nil)
		return
	}

	msgID := req.FormValue("id")
	// Check authorization: either auth information or SID must be present
	uid, challenge, err := authHttpRequest(req)
	if err != nil {
		writeHttpResponse(decodeStoreError(err, msgID, "", now, now, nil), err)
		return
	}
	if challenge != nil {
		writeHttpResponse(InfoChallenge(msgID, now, challenge), nil)
		return
	}
	if uid.IsZero() {
		// Not authenticated
		writeHttpResponse(ErrAuthRequired(msgID, "", now), nil)
		return
	}

	// Check if uploads are handled elsewhere.
	if redirTo, err := mh.Redirect(req.Method, req.URL.String()); redirTo != "" {
		wrt.Header().Set("Location", redirTo)
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.WriteHeader(http.StatusTemporaryRedirect)
		enc.Encode(InfoFound("", "", now))

		log.Println("media upload redirected", redirTo)
		return
	} else if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, now, nil), err)
		return
	}

	file, _, err := req.FormFile("file")
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			writeHttpResponse(ErrTooLarge(msgID, "", now), err)
		} else {
			writeHttpResponse(ErrMalformed(msgID, "", now), err)
		}
		return
	}
	fdef := types.FileDef{}
	fdef.Id = store.GetUidString()
	fdef.InitTimes()
	fdef.User = uid.String()

	buff := make([]byte, 512)
	if _, err = file.Read(buff); err != nil {
		writeHttpResponse(ErrUnknown(msgID, "", now), err)
		return
	}

	fdef.MimeType = http.DetectContentType(buff)
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		writeHttpResponse(ErrUnknown(msgID, "", now), err)
		return
	}

	url, err := mh.Upload(&fdef, file)
	if err != nil {
		writeHttpResponse(decodeStoreError(err, msgID, "", now, now, nil), err)
		return
	}

	writeHttpResponse(NoErrParams(msgID, "", now, map[string]string{"url": url}), nil)
}

func largeFileRunGarbageCollection(period time.Duration, block int) chan<- bool {
	// Unbuffered stop channel. Whoever stops it must wait for the process to finish.
	stop := make(chan bool)
	go func() {
		gcTimer := time.Tick(period)
		for {
			select {
			case <-gcTimer:
				if err := store.Files.DeleteUnused(time.Now().Add(-time.Hour), block); err != nil {
					log.Println("media gc:", err)
				}
			case <-stop:
				return
			}
		}
	}()

	return stop
}
