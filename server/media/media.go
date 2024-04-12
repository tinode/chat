// Package media defines an interface which must be implemented by media upload/download handlers.
package media

import (
	"io"
	"net/http"
	"path"
	"regexp"
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

	// Upload processes request for file upload. Returns file URL, file size, error.
	Upload(fdef *types.FileDef, file io.ReadSeeker) (string, int64, error)

	// Download processes request for file download.
	Download(url string) (*types.FileDef, ReadSeekCloser, error)

	// Delete deletes file from storage.
	Delete(locations []string) error

	// GetIdFromUrl extracts file ID from download URL.
	GetIdFromUrl(url string) types.Uid
}

var fileNamePattern = regexp.MustCompile(`^[-_A-Za-z0-9]+`)

// GetIdFromUrl is a helper method for extracting file ID from a URL.
func GetIdFromUrl(url, serveUrl string) types.Uid {
	dir, fname := path.Split(path.Clean(url))

	if dir != "" && dir != serveUrl {
		return types.ZeroUid
	}

	return types.ParseUid(fileNamePattern.FindString(fname))
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

	origin = strings.ToLower(origin)
	for _, val := range allowed {
		if strings.ToLower(val) == origin {
			return origin
		}
	}

	return ""
}

// allowMethods must be in UPPERCASE.
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

// CORSHandler is the default CORS processor for use by media handlers. It adds CORS headers to
// preflight OPTIONS requests, Vary & Access-Control-Allow-Origin headers to all responses.
func CORSHandler(req *http.Request, allowedOrigins []string, serve bool) (http.Header, int) {
	headers := map[string][]string{
		// Always add Vary because of possible intermediate caches.
		"Vary": {"Origin", "Access-Control-Request-Method, Access-Control-Request-Headers"},
	}

	origin := req.Header.Get("Origin")

	allowedOrigin := matchCORSOrigin(allowedOrigins, origin)
	requestMethod := req.Header.Get("Access-Control-Request-Method")
	if req.Method == http.MethodOptions && requestMethod != "" {
		// Preflight request.

		if allowedOrigin == "" {
			return headers, http.StatusNoContent
		}

		var allowMethods []string
		if serve {
			allowMethods = []string{http.MethodGet, http.MethodHead, http.MethodOptions}
		} else {
			allowMethods = []string{http.MethodPost, http.MethodPut, http.MethodHead, http.MethodOptions}
		}

		if !matchCORSMethod(allowMethods, requestMethod) {
			// CORS policy does not allow this method.
			return headers, http.StatusNoContent
		}

		headers["Access-Control-Allow-Headers"] = []string{"*"}
		headers["Access-Control-Allow-Credentials"] = []string{"true"}
		headers["Access-Control-Allow-Methods"] = []string{strings.Join(allowMethods, ", ")}
		headers["Access-Control-Max-Age"] = []string{"86400"}
		headers["Access-Control-Allow-Origin"] = []string{allowedOrigin}

		return headers, http.StatusNoContent
	}

	// Regular request, not a preflight.

	if allowedOrigin != "" {
		// Returning Origin from the actual request instead of '*', otherwise there could be an issue with Credentials.
		headers["Access-Control-Allow-Origin"] = []string{origin}
	}

	return headers, 0
}
