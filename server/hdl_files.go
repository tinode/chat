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
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/pbx"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
	"google.golang.org/grpc/peer"
)

// Allowed mime types for user-provided Content-type field. Must be alphabetically sorted.
// Types not in the list are converted to "application/octet-stream".
// See https://www.iana.org/assignments/media-types/media-types.xhtml
var allowedMimeTypes = []string{"application/", "audio/", "font/", "image/", "text/", "video/"}

func largeFileServeHTTP(wrt http.ResponseWriter, req *http.Request) {
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
			logs.Warn.Println("media serve:", req.URL.String(), err)
		}
	}

	// Preflight request: process before any security checks.
	if req.Method == http.MethodOptions {
		headers, statusCode, err := mh.Headers(req.Method, req.URL, req.Header, true)
		if err != nil {
			writeHttpResponse(decodeStoreError(err, "", now, nil), err)
			return
		}
		for name, values := range headers {
			for _, value := range values {
				wrt.Header().Add(name, value)
			}
		}
		if statusCode <= 0 {
			statusCode = http.StatusNoContent
		}
		wrt.WriteHeader(statusCode)
		logs.Info.Println("media serve: preflight completed")
		return
	}

	// Check if this is a GET/HEAD request.
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		writeHttpResponse(ErrOperationNotAllowed("", "", now), errors.New("method '"+req.Method+"' not allowed"))
		return
	}

	// Check for API key presence
	if isValid, _ := checkAPIKey(getAPIKey(req)); !isValid {
		writeHttpResponse(ErrAPIKeyRequired(now), errors.New("invalid or missing API key"))
		return
	}

	// Check authorization: either auth information or SID must be present
	authMethod, secret := getHttpAuth(req)
	uid, challenge, err := authFileRequest(authMethod, secret, req.FormValue("sid"), getRemoteAddr(req))
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", now, nil), err)
		return
	}

	if challenge != nil {
		writeHttpResponse(InfoChallenge("", now, challenge), nil)
		return
	}

	if uid.IsZero() {
		// Not authenticated
		writeHttpResponse(ErrAuthRequired("", "", now, now), errors.New("user not authenticated"))
		return
	}

	// Check if media handler redirects or adds headers.
	headers, statusCode, err := mh.Headers(req.Method, req.URL, req.Header, true)
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", now, nil), err)
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
		logs.Info.Println("media serve: completed with status", statusCode, "uid=", uid)
		return
	}

	if req.Method == http.MethodHead {
		wrt.WriteHeader(http.StatusOK)
		logs.Info.Println("media serve: completed", req.Method, "uid=", uid)
		return
	}

	fd, rsc, err := mh.Download(req.URL.String())
	if err != nil {
		writeHttpResponse(decodeStoreError(err, "", now, nil), err)
		return
	}

	defer rsc.Close()

	wrt.Header().Set("Content-Type", fd.MimeType)
	asAttachment, _ := strconv.ParseBool(req.URL.Query().Get("asatt"))
	// Force download for html files as a security measure.
	asAttachment = asAttachment ||
		strings.Contains(fd.MimeType, "html") ||
		strings.Contains(fd.MimeType, "xml") ||
		strings.HasPrefix(fd.MimeType, "application/") ||
		// The 'message', 'model', and 'multipart' cannot currently appear, but checked anyway in case
		// DetectContentType changes its logic.
		strings.HasPrefix(fd.MimeType, "message/") ||
		strings.HasPrefix(fd.MimeType, "model/") ||
		strings.HasPrefix(fd.MimeType, "multipart/") ||
		strings.HasPrefix(fd.MimeType, "text/")
	if asAttachment {
		wrt.Header().Set("Content-Disposition", "attachment")
	}

	http.ServeContent(wrt, req, "", fd.UpdatedAt, rsc)

	logs.Info.Println("media serve: OK, uid=", uid)
}

// largeFileReceiveHTTP receives files from client over HTTP(S) and passes them to the configured media handler.
func largeFileReceiveHTTP(wrt http.ResponseWriter, req *http.Request) {
	now := types.TimeNow()
	enc := json.NewEncoder(wrt)
	mh := store.Store.GetMediaHandler()
	statsInc("FileUploadsTotal", 1)

	writeHttpResponse := func(msg *ServerComMessage, err error) {
		// Gorilla CompressHandler requires Content-Type to be set.
		wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
		wrt.WriteHeader(msg.Ctrl.Code)
		enc.Encode(msg)

		if err != nil {
			logs.Info.Println("media upload:", msg.Ctrl.Code, msg.Ctrl.Text, "/", err)
		}
	}

	// Preflight request: process before any security checks.
	if req.Method == http.MethodOptions {
		headers, statusCode, err := mh.Headers(req.Method, req.URL, req.Header, true)
		if err != nil {
			writeHttpResponse(decodeStoreError(err, "", now, nil), err)
			return
		}
		for name, values := range headers {
			for _, value := range values {
				wrt.Header().Add(name, value)
			}
		}
		if statusCode <= 0 {
			statusCode = http.StatusNoContent
		}
		wrt.WriteHeader(statusCode)
		logs.Info.Println("media upload: preflight completed")
		return
	}

	// Check if this is a POST/PUT/HEAD request.
	if req.Method != http.MethodPost && req.Method != http.MethodPut && req.Method != http.MethodHead {
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
	authMethod, secret := getHttpAuth(req)
	uid, challenge, err := authFileRequest(authMethod, secret, req.FormValue("sid"), getRemoteAddr(req))
	if err != nil {
		writeHttpResponse(decodeStoreError(err, msgID, now, nil), err)
		return
	}
	if challenge != nil {
		writeHttpResponse(InfoChallenge(msgID, now, challenge), nil)
		return
	}
	if uid.IsZero() && req.FormValue("topic") != "newacc" {
		// Not authenticated and not signup.
		writeHttpResponse(ErrAuthRequired(msgID, "", now, now), nil)
		return
	}

	// Check if uploads are handled elsewhere.
	headers, statusCode, err := mh.Headers(req.Method, req.URL, req.Header, true)
	if err != nil {
		logs.Info.Println("media upload: headers check failed", err)
		writeHttpResponse(decodeStoreError(err, "", now, nil), err)
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

	file, header, err := req.FormFile("file")
	if err != nil {
		logs.Info.Println("media upload: invalid multipart form", err)
		if strings.Contains(err.Error(), "request body too large") {
			writeHttpResponse(ErrTooLarge(msgID, "", now), err)
		} else {
			writeHttpResponse(ErrMalformed(msgID, "", now), err)
		}
		return
	}

	buff := make([]byte, 512)
	if _, err = file.Read(buff); err != nil {
		writeHttpResponse(ErrUnknown(msgID, "", now), err)
		return
	}

	mimeType := http.DetectContentType(buff)
	// If DetectContentType fails, see if client-provided content type can be used.
	if mimeType == "application/octet-stream" {
		if userContentType, params, err := mime.ParseMediaType(header.Header.Get("Content-Type")); err == nil {
			// Make sure the content-type is legit.
			for _, allowed := range allowedMimeTypes {
				if strings.HasPrefix(userContentType, allowed) {
					if userContentType = mime.FormatMediaType(userContentType, params); userContentType != "" {
						mimeType = userContentType
					}
					break
				}
			}
		}
	}

	fdef := &types.FileDef{
		ObjHeader: types.ObjHeader{
			Id: store.Store.GetUidString(),
		},
		User:     uid.String(),
		MimeType: mimeType,
	}
	fdef.InitTimes()

	if _, err = file.Seek(0, io.SeekStart); err != nil {
		writeHttpResponse(ErrUnknown(msgID, "", now), err)
		return
	}

	url, size, err := mh.Upload(fdef, file)
	if err != nil {
		logs.Info.Println("media upload: failed", file, "key", fdef.Location, err)
		store.Files.FinishUpload(fdef, false, 0)
		writeHttpResponse(decodeStoreError(err, msgID, now, nil), err)
		return
	}

	fdef, err = store.Files.FinishUpload(fdef, true, size)
	if err != nil {
		logs.Info.Println("media upload: failed to finalize", file, "key", fdef.Location, err)
		// Best effort cleanup.
		mh.Delete([]string{fdef.Location})
		writeHttpResponse(decodeStoreError(err, msgID, now, nil), err)
		return
	}

	params := map[string]string{"url": url}
	if globals.mediaGcPeriod > 0 {
		// How long this file is guaranteed to exist without being attached to a message or a topic.
		params["expires"] = now.Add(globals.mediaGcPeriod).Format(types.TimeFormatRFC3339)
	}

	writeHttpResponse(NoErrParams(msgID, "", now, params), nil)
	logs.Info.Println("media upload: ok", fdef.Id, fdef.Location)
}

// LargeFileServe is the gRPC equivalent of largeFileServeHTTP.
func (*grpcNodeServer) LargeFileServe(req *pbx.FileDownReq, stream pbx.Node_LargeFileServeServer) error {
	now := types.TimeNow()

	writeResponse := func(msg *ServerComMessage, err error) {
		stream.Send(&pbx.FileDownResp{Id: msg.Ctrl.Id, Code: int32(msg.Ctrl.Code), Text: msg.Ctrl.Text})
		if err != nil {
			logs.Info.Println("media serve:", msg.Ctrl.Code, msg.Ctrl.Text, "/", err)
		}
	}

	msgID := req.GetId()

	// Check authorization: auth information must be present (SID is not used for gRPC).
	authMethod, secret := req.Auth.Scheme, req.Auth.Secret
	var remoteAddr string
	if p, ok := peer.FromContext(stream.Context()); ok {
		remoteAddr = p.Addr.String()
	}
	uid, challenge, err := authFileRequest(authMethod, secret, "", remoteAddr)
	if err != nil {
		writeResponse(decodeStoreError(err, msgID, now, nil), err)
		return nil
	}

	if challenge != nil {
		writeResponse(InfoChallenge(msgID, now, challenge), nil)
		return nil
	}

	if uid.IsZero() {
		// Not authenticated
		writeResponse(ErrAuthRequired(msgID, "", now, now), errors.New("user not authenticated"))
		return nil
	}

	// Check if media handler redirects or adds headers.
	mh := store.Store.GetMediaHandler()
	url, _ := url.Parse(req.Uri)
	headers, statusCode, err := mh.Headers(http.MethodGet, url, http.Header{}, true)
	if err != nil {
		writeResponse(decodeStoreError(err, "", now, nil), err)
		return nil
	}

	resp := pbx.FileDownResp{Meta: &pbx.FileMeta{}}
	if statusCode != 0 {
		// The handler requested to terminate further processing.
		resp.Code = int32(statusCode)
		resp.Text = http.StatusText(statusCode)
		resp.RedirUrl = headers.Get("Location")
		stream.Send(&resp)
		logs.Info.Println("media serve: completed with status", statusCode, "uid=", uid)
		return nil
	}

	fd, rsc, err := mh.Download(req.GetUri())
	if err != nil {
		writeResponse(decodeStoreError(err, msgID, now, nil), err)
		return nil
	}

	defer rsc.Close()

	resp.Code = http.StatusOK
	resp.Text = http.StatusText(http.StatusOK)
	resp.Meta.Name = fd.Location
	resp.Meta.MimeType = fd.MimeType
	resp.Meta.Size = fd.Size

	resp.Content = make([]byte, 1024*1024*2)
	var n int
	result := "OK"
	for {
		n, err = rsc.Read(resp.Content)
		if err == nil {
			resp.Content = resp.Content[:n]
			if err = stream.Send(&resp); err != nil {
				logs.Info.Println("media serve: failed, uid=", uid, err)
				break
			}
			continue
		}
		if err == io.EOF {
			err = nil
		} else {
			result = err.Error()
		}
		break
	}
	logs.Info.Println("media serve: ", result, ", uid=", uid)
	return err
}

// LargeFileReceive is the gRPC equivalent of LargeFileReceiveHTTP.
func (*grpcNodeServer) LargeFileReceive(stream pbx.Node_LargeFileReceiveServer) error {
	now := types.TimeNow()
	mh := store.Store.GetMediaHandler()

	writeResponse := func(msg *ServerComMessage, err error) {
		stream.SendAndClose(&pbx.FileUpResp{Id: msg.Ctrl.Id, Code: int32(msg.Ctrl.Code), Text: msg.Ctrl.Text})
		if err != nil {
			logs.Info.Println("media receive:", msg.Ctrl.Code, msg.Ctrl.Text, "/", err)
		}
	}

	req, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			writeResponse(ErrDisconnected("", "", now), err)
		} else {
			writeResponse(decodeStoreError(err, "", now, nil), err)
		}
		return nil
	}

	msgID := req.GetId()
	// Check authorization: auth information must be present (SID is not used for gRPC).
	authMethod, secret := req.Auth.Scheme, req.Auth.Secret
	var remoteAddr string
	if p, ok := peer.FromContext(stream.Context()); ok {
		remoteAddr = p.Addr.String()
	}
	uid, challenge, err := authFileRequest(authMethod, secret, "", remoteAddr)
	if err != nil {
		writeResponse(decodeStoreError(err, msgID, now, nil), err)
		return nil
	}

	if challenge != nil {
		writeResponse(InfoChallenge(msgID, now, challenge), nil)
		return nil
	}

	if uid.IsZero() {
		// Not authenticated
		writeResponse(ErrAuthRequired(msgID, "", now, now), errors.New("user not authenticated"))
		return nil
	}

	// Check if uploads are handled elsewhere.
	headers, statusCode, err := mh.Headers(http.MethodPost, nil, http.Header{}, false)
	if err != nil {
		logs.Info.Println("media upload: headers check failed", err)
		writeResponse(decodeStoreError(err, "", now, nil), nil)
		return nil
	}

	if statusCode != 0 {
		// The handler requested to terminate further processing.
		err = stream.SendAndClose(&pbx.FileUpResp{
			Id:       msgID,
			Code:     int32(statusCode),
			Text:     http.StatusText(statusCode),
			RedirUrl: headers.Get("Location"),
		})
		logs.Info.Println("media upload: completed with status", statusCode, "uid=", uid, err)
		return err
	}

	mimeType := http.DetectContentType(req.Content)
	// If DetectContentType fails, use client-provided content type.
	if mimeType == "application/octet-stream" {
		if contentType := req.Meta.GetMimeType(); contentType != "" {
			mimeType = contentType
		}
	}

	fdef := &types.FileDef{
		ObjHeader: types.ObjHeader{
			Id: store.Store.GetUidString(),
		},
		User:     uid.String(),
		MimeType: mimeType,
	}
	fdef.InitTimes()

	reader, writer := io.Pipe()
	// Create a non-blocking channel to collect errors from the inbound IO process.
	done := make(chan error, 1)
	go func() {
		defer writer.Close()
		for {
			if req, err := stream.Recv(); err == nil {
				chunk := req.GetContent()
				if _, err := writer.Write(chunk); err != nil {
					done <- err
					break
				}
			} else {
				if err == io.EOF {
					err = nil
				}
				done <- err
				break
			}
		}
	}()

	url, size, err := mh.Upload(fdef, reader)
	if err == nil {
		// No outbound IO error. Maybe we have an inbound one?
		err = <-done
	}
	if err != nil {
		logs.Info.Println("media upload: failed", req.Meta.Name, "key", fdef.Location, err)
		store.Files.FinishUpload(fdef, false, 0)
		writeResponse(decodeStoreError(err, msgID, now, nil), nil)
		return nil
	}

	err = stream.SendAndClose(&pbx.FileUpResp{
		Id:   msgID,
		Code: http.StatusOK,
		Text: http.StatusText(http.StatusOK),
		Meta: &pbx.FileMeta{
			Name:     url,
			MimeType: mimeType,
			Etag:     fdef.ETag,
			Size:     size,
		},
	})
	logs.Info.Println("media upload: ok", fdef.Id, fdef.Location, err)
	return err
}

// largeFileRunGarbageCollection runs every 'period' and deletes up to 'blockSize' unused files.
// Returns channel which can be used to stop the process.
func largeFileRunGarbageCollection(period time.Duration, blockSize int) chan<- bool {
	// Unbuffered stop channel. Whomever stops the gc must wait for the process to finish.
	stop := make(chan bool)
	go func() {
		// Add some randomness to the tick period to desynchronize runs on cluster nodes:
		// 0.75 * period + rand(0, 0.5) * period.
		period = (period >> 1) + (period >> 2) + time.Duration(rand.Intn(int(period>>1)))
		gcTicker := time.Tick(period)
		for {
			select {
			case <-gcTicker:
				if err := store.Files.DeleteUnused(time.Now().Add(-time.Hour), blockSize); err != nil {
					logs.Warn.Println("media gc:", err)
				}
			case <-stop:
				return
			}
		}
	}()

	return stop
}

// Authenticate non-websocket HTTP request
func authFileRequest(authMethod, secret, sid, remoteAddr string) (types.Uid, []byte, error) {
	var uid types.Uid
	if authMethod != "" {
		decodedSecret := make([]byte, base64.StdEncoding.DecodedLen(len(secret)))
		n, err := base64.StdEncoding.Decode(decodedSecret, []byte(secret))
		if err != nil {
			logs.Info.Println("media: invalid auth secret", authMethod, "'"+secret+"'")
			return uid, nil, types.ErrMalformed
		}

		if authhdl := store.Store.GetLogicalAuthHandler(authMethod); authhdl != nil {
			rec, challenge, err := authhdl.Authenticate(decodedSecret[:n], remoteAddr)
			if err != nil {
				return uid, nil, err
			}
			if challenge != nil {
				return uid, challenge, nil
			}
			uid = rec.Uid
		} else {
			logs.Info.Println("media: unknown auth method", authMethod)
			return uid, nil, types.ErrMalformed
		}
	} else {
		// Find the session, make sure it's appropriately authenticated.
		sess := globals.sessionStore.Get(sid)
		if sess != nil {
			uid = sess.uid
		}
	}
	return uid, nil, nil
}
