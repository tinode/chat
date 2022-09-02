// Debug tooling. Dumps named profile in response to HTTP request at
// 		http(s)://<host-name>/<configured-path>/<profile-name>
// See godoc for the list of possible profile names: https://golang.org/pkg/runtime/pprof/#Profile

package main

import (
	"net/http"
	"net/http/pprof"
	"path"

	"github.com/tinode/chat/server/logs"
)

// Expose debug profiling at the given URL path.
func servePprof(mux *http.ServeMux, serveAt string) {
	if serveAt == "" || serveAt == "-" {
		return
	}

	pprofRoot := path.Clean("/"+serveAt) + "/"

	mux.HandleFunc(pprofRoot, pprof.Index)
	mux.HandleFunc(path.Join(pprofRoot, "cmdline"), pprof.Cmdline)
	mux.HandleFunc(path.Join(pprofRoot, "profile"), pprof.Profile)
	mux.HandleFunc(path.Join(pprofRoot, "symbol"), pprof.Symbol)
	mux.HandleFunc(path.Join(pprofRoot, "trace"), pprof.Trace)

	logs.Info.Printf("pprof: profiling info exposed at '%s'", pprofRoot)
}
