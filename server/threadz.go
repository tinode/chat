// Debugging threading issues: dumps stacktraces of all goroutines in response to
// HTTP request to the given path.

package main

import (
	"log"
	"net/http"
	"runtime/pprof"
	"strings"
)

// Initialize stack tracing at the given URL path.
func threadzInit(mux *http.ServeMux, path string) {
	if path == "" || path == "-" {
		return
	}

	// Make sure the path is absolute.
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	mux.HandleFunc(path, threadzHandler)

	log.Printf("threadz: stack traces exposed at '%s'", path)
}

// Handler which dumps stacktraces of all goroutines to response writer.
func threadzHandler(wrt http.ResponseWriter, req *http.Request) {
	wrt.Header().Set("Content-Type", "text/plain; charset=utf-8")
	pprof.Lookup("goroutine").WriteTo(wrt, 2)
}
