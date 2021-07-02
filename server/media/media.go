// Package media defines an interface which must be implemented by media upload/download handlers.
package media

import (
	"io"
	"net/http"
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

	// Headers checks if the handler wants to provide additional HTTP headers for the request.
	// It could be CORS headers, redirect to serve files from another URL, cache-control headers.
	// It returns headers as a map, HTTP status code to stop processing or 0 to continue, error.
	Headers(req *http.Request, serve bool) (http.Header, int, error)

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
func GetIdFromUrl(url, serveUrl string) types.Uid {
	dir, fname := path.Split(path.Clean(url))

	if dir != "" && dir != serveUrl {
		return types.ZeroUid
	}

	return types.ParseUid(strings.Split(fname, ".")[0])
}

// matchCORSOrigin compares origin from the HTTP request to a list of allowed origins.
func matchCORSOrigin(allowed []string, origin string) string {
	if origin == "" {
		// Request has no Origin header.
		return ""
	}

	if len(allowed) == 0 {
		// Not configured
		return ""
	}

	if allowed[0] == "*" {
		return "*"
	}

	for _, val := range allowed {
		if val == origin {
			return origin
		}
	}

	return ""
}

func matchCORSMethod(allowMethods []string, method string) bool {
	if method == "" {
		// Request has no Method header.
		return false
	}

	method = strings.ToUpper(method)
	for _, mm := range allowMethods {
		if mm == method {
			return true
		}
	}

	return false
}

// CORSHandler is the default preflight OPTIONS processor for use by media handlers.
func CORSHandler(req *http.Request, allowedOrigins []string, serve bool) (http.Header, int) {
	if req.Method != http.MethodOptions {
		// Not an OPTIONS request. No special handling for all other requests.
		return nil, 0
	}

	headers := map[string][]string{
		// Always add Vary because of possible intermediate caches.
		"Vary": []string{"Origin", "Access-Control-Request-Method"},
	}

	allowedOrigin := matchCORSOrigin(allowedOrigins, req.Header.Get("Origin"))
	if allowedOrigin == "" {
		// CORS policy does not match the origin.
		return headers, http.StatusOK
	}

	var allowMethods []string
	if serve {
		allowMethods = []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	} else {
		allowMethods = []string{http.MethodPost, http.MethodPut, http.MethodHead, http.MethodOptions}
	}

	if !matchCORSMethod(allowMethods, req.Header.Get("Access-Control-Request-Method")) {
		// CORS policy does not allow this method.
		return headers, http.StatusOK
	}

	headers["Access-Control-Allow-Origin"] = []string{allowedOrigin}
	headers["Access-Control-Allow-Headers"] = []string{"*"}
	headers["Access-Control-Allow-Methods"] = []string{strings.Join(allowMethods, ",")}
	headers["Access-Control-Max-Age"] = []string{"86400"}
	headers["Access-Control-Allow-Credentials"] = []string{"true"}

	return headers, http.StatusOK
}
