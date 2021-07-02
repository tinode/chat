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
	"net/http"
	"strings"
	"time"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

func largeFileServe(wrt http.ResponseWriter, req *http.Request) {
	now := types.TimeNow()
	enc := json.NewEncoder(wrt)
	mh := store.Store.GetMediaHandler()
	statsInc("FileDownloadsTotal", 1)

	writeHttpResponse := func(msg *ServerComMessage, err error) {
		// Gorilla CompressHandler requires Content-Type to be set.
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.WriteHeader(msg.Ctrl.Code)
		enc.Encode(msg)
		if err != nil {
			logs.Warn.Println("media serve:", err)
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
		writeHttpResponse(decodeStoreError(err, "", "", now, nil), err)
		return
	}
	if challenge != nil {
		writeHttpResponse(InfoChallenge("", now, challenge), nil)
		return
	}
	if uid.IsZero() {
		// Not authenticated
		writeHttpResponse(ErrAuthRequired("", "", now, now), nil)
		return
	}

	// Check if this is a GET/OPTIONS/HEAD request.
	if req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodOptions {
		writeHttpResponse(ErrOperationNotAllowed("", "", now), errors.New("method '"+req.Method+"' not allowed"))
		return
	}

	// Check if media handler redirects or adds headers.
	headers, statusCode, err := mh.Headers(req, true)
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, nil), err)
		return
	}

	for name, values := range headers {
		for _, value := range values {
			wrt.Header().Add(name, value)
		}
	}

	if statusCode != 0 {
		// The handler requested to terminate further processing.
		wrt.WriteHeader(statusCode)
		if req.Method == http.MethodGet {
			enc.Encode(&ServerComMessage{
				Ctrl: &MsgServerCtrl{
					Code:      statusCode,
					Text:      http.StatusText(statusCode),
					Timestamp: now,
				},
			})
		}
		logs.Info.Println("media serve: completed with status", statusCode)
		return
	}

	if req.Method == http.MethodHead || req.Method == http.MethodOptions {
		wrt.WriteHeader(http.StatusOK)
		logs.Info.Println("media serve: completed", req.Method)
		return
	}

	fd, rsc, err := mh.Download(req.URL.String())
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, nil), err)
		return
	}

	defer rsc.Close()

	wrt.Header().Set("Content-Type", fd.MimeType)
	wrt.Header().Set("Content-Disposition", "attachment")
	http.ServeContent(wrt, req, "", fd.UpdatedAt, rsc)

	logs.Info.Println("media serve: OK")
}

// largeFileReceive receives files from client over HTTP(S) and passes them to the configured media handler.
func largeFileReceive(wrt http.ResponseWriter, req *http.Request) {
	now := types.TimeNow()
	enc := json.NewEncoder(wrt)
	mh := store.Store.GetMediaHandler()
	statsInc("FileUploadsTotal", 1)

	writeHttpResponse := func(msg *ServerComMessage, err error) {
		// Gorilla CompressHandler requires Content-Type to be set.
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.WriteHeader(msg.Ctrl.Code)
		enc.Encode(msg)

		logs.Info.Println("media upload:", msg.Ctrl.Code, msg.Ctrl.Text, "/", err)
	}

	// Check if this is a POST/PUT/OPTIONS/HEAD request.
	if req.Method != http.MethodPost && req.Method != http.MethodPut &&
		req.Method != http.MethodHead && req.Method != http.MethodOptions {
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
		writeHttpResponse(decodeStoreError(err, msgID, "", now, nil), err)
		return
	}
	if challenge != nil {
		writeHttpResponse(InfoChallenge(msgID, now, challenge), nil)
		return
	}
	if uid.IsZero() {
		// Not authenticated
		writeHttpResponse(ErrAuthRequired(msgID, "", now, now), nil)
		return
	}

	// Check if uploads are handled elsewhere.
	headers, statusCode, err := mh.Headers(req, false)
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", "", now, nil), err)
		return
	}

	for name, values := range headers {
		for _, value := range values {
			wrt.Header().Add(name, value)
		}
	}

	if statusCode != 0 {
		// The handler requested to terminate further processing.
		wrt.WriteHeader(statusCode)
		if req.Method == http.MethodPost || req.Method == http.MethodPut {
			enc.Encode(&ServerComMessage{
				Ctrl: &MsgServerCtrl{
					Code:      statusCode,
					Text:      http.StatusText(statusCode),
					Timestamp: now,
				},
			})
		}
		logs.Info.Println("media upload: completed with status", statusCode)
		return
	}

	if req.Method == http.MethodHead || req.Method == http.MethodOptions {
		wrt.WriteHeader(http.StatusOK)
		logs.Info.Println("media upload: completed", req.Method)
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
	fdef.Id = store.Store.GetUidString()
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
		writeHttpResponse(decodeStoreError(err, msgID, "", now, nil), err)
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
					logs.Warn.Println("media gc:", err)
				}
			case <-stop:
				return
			}
		}
	}()

	return stop
}
