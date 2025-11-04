// Package media defines an interface which must be implemented by media upload/download handlers.
package media

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"slices"

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
	Headers(method string, url *url.URL, headers http.Header, serve bool) (http.Header, int, error)

	// Upload processes request for file upload. Returns file URL, file size, error.
	Upload(fdef *types.FileDef, file io.Reader) (string, int64, error)

	// Download processes request for file download.
	Download(url string) (*types.FileDef, ReadSeekCloser, error)

	// Delete deletes file from storage.
	Delete(locations []string) error

	// GetIdFromUrl extracts file ID from download URL.
	GetIdFromUrl(url string) types.Uid
}

type AllowedOrigin struct {
	Origin      string
	URL         url.URL
	HostParts   []string
	HasWildcard bool
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

// ParseCORSAllow pre-parses allowed origins from the configuration.
func ParseCORSAllow(allowed []string) ([]AllowedOrigin, error) {
	if len(allowed) == 0 {
		return nil, nil
	}

	result := make([]AllowedOrigin, 0, len(allowed))
	for _, val := range allowed {
		parsed := AllowedOrigin{Origin: val}
		switch val {
		case "*":
			if len(allowed) > 1 {
				return nil, errors.New("wildcard origin '*' must be the only entry")
			}
			parsed.HasWildcard = true
		case "":
			if len(allowed) > 1 {
				return nil, errors.New("empty allowed origin '' must be the only entry")
			}
			// Empty string means no origin allowed - no URL parsing needed
			parsed.HasWildcard = false
		default:
			u, err := url.ParseRequestURI(val)
			if err != nil {
				return nil, err
			}
			parsed.HostParts = strings.Split(u.Hostname(), ".")
			parsed.URL = *u
			parsed.HasWildcard = strings.Contains(u.Hostname(), "*")
		}
		result = append(result, parsed)
	}
	return result, nil
}

// matchCORSOrigin compares origin from the HTTP request to a list of allowed origins.
func matchCORSOrigin(allowed []AllowedOrigin, origin string) string {
	if len(allowed) == 0 {
		// Not configured
		return ""
	}

	if origin == "" && allowed[0].Origin != "*" {
		// Request has no Origin header and "*" (any origin) not allowed.
		return ""
	}

	if allowed[0].Origin == "*" {
		if origin == "" {
			return "*"
		}
		return origin
	}

	// Check for empty string in allowed origins - this means no origin is allowed.
	if allowed[0].Origin == "" {
		return ""
	}

	originUrl, err := url.ParseRequestURI(origin)
	if err != nil {
		return ""
	}
	originParts := strings.Split(originUrl.Hostname(), ".")

	for _, val := range allowed {
		if val.Origin == origin {
			return origin
		}

		if !val.HasWildcard ||
			originUrl.Scheme != val.URL.Scheme ||
			originUrl.Port() != val.URL.Port() ||
			len(originParts) != len(val.HostParts) {
			continue
		}

		matched := true
		for i, part := range val.HostParts {
			if part == "*" {
				continue
			}
			if part != originParts[i] {
				matched = false
				break
			}
		}
		if matched {
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

	return slices.Contains(allowMethods, strings.ToUpper(method))
}

// CORSHandler is the default CORS processor for use by media handlers. It adds CORS headers to
// preflight OPTIONS requests, Vary & Access-Control-Allow-Origin headers to all responses.
func CORSHandler(method string, reqHeader http.Header, allowedOrigins []AllowedOrigin, serve bool) (http.Header, int) {
	respHeader := map[string][]string{
		// Always add Vary because of possible intermediate caches.
		"Vary": {"Origin", "Access-Control-Request-Method, Access-Control-Request-Headers"},
	}

	origin := reqHeader.Get("Origin")

	allowedOrigin := matchCORSOrigin(allowedOrigins, origin)
	if acMethod := reqHeader.Get("Access-Control-Request-Method"); method == http.MethodOptions && acMethod != "" {
		// Preflight request.

		if allowedOrigin == "" {
			return respHeader, http.StatusNoContent
		}

		var allowMethods []string
		if serve {
			allowMethods = []string{http.MethodGet, http.MethodHead, http.MethodOptions}
		} else {
			allowMethods = []string{http.MethodPost, http.MethodPut, http.MethodHead, http.MethodOptions}
		}

		if !matchCORSMethod(allowMethods, acMethod) {
			// CORS policy does not allow this method.
			return respHeader, http.StatusNoContent
		}

		respHeader["Access-Control-Allow-Headers"] = []string{"*"}
		respHeader["Access-Control-Allow-Credentials"] = []string{"true"}
		respHeader["Access-Control-Allow-Methods"] = []string{strings.Join(allowMethods, ", ")}
		respHeader["Access-Control-Max-Age"] = []string{"86400"}
		respHeader["Access-Control-Allow-Origin"] = []string{allowedOrigin}

		return respHeader, http.StatusNoContent
	}

	// Regular request, not a preflight.

	if allowedOrigin != "" {
		// Returning Origin from the actual request instead of '*', otherwise there could be an issue with Credentials.
		respHeader["Access-Control-Allow-Origin"] = []string{origin}
	}

	return respHeader, 0
}
