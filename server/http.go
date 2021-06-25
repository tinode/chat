/******************************************************************************
 *
 *  Description :
 *
 *  Web server initialization and shutdown.
 *
 *****************************************************************************/

package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

func listenAndServe(addr string, mux *http.ServeMux, tlfConf *tls.Config, stop <-chan bool) error {
	globals.shuttingDown = false

	httpdone := make(chan bool)

	server := &http.Server{
		Handler: mux,
	}

	server.TLSConfig = tlfConf

	go func() {
		var err error
		if server.TLSConfig != nil {
			// If port is not specified, use default https port (443),
			// otherwise it will default to 80
			if addr == "" {
				addr = ":https"
			}

			if globals.tlsRedirectHTTP != "" {
				// Serving redirects from a unix socket or to a unix socket makes no sense.
				if isUnixAddr(globals.tlsRedirectHTTP) || isUnixAddr(addr) {
					err = errors.New("HTTP to HTTPS redirect: unix sockets not supported")
				} else {
					logs.Info.Printf("Redirecting connections from HTTP at [%s] to HTTPS at [%s]",
						globals.tlsRedirectHTTP, addr)

					// This is a second HTTP server listenning on a different port.
					go http.ListenAndServe(globals.tlsRedirectHTTP, tlsRedirect(addr))
				}
			}

			if err == nil {
				logs.Info.Printf("Listening for client HTTPS connections on [%s]", addr)
				var lis net.Listener
				lis, err = netListener(addr)
				if err == nil {
					err = server.ServeTLS(lis, "", "")
				}
			}
		} else {
			logs.Info.Printf("Listening for client HTTP connections on [%s]", addr)
			var lis net.Listener
			lis, err = netListener(addr)
			if err == nil {
				err = server.Serve(lis)
			}
		}

		if err != nil {
			if globals.shuttingDown {
				logs.Info.Println("HTTP server: stopped")
			} else {
				logs.Err.Println("HTTP server: failed", err)
			}
		}
		httpdone <- true
	}()

	// Wait for either a termination signal or an error
Loop:
	for {
		select {
		case <-stop:
			// Flip the flag that we are terminating and close the Accept-ing socket, so no new connections are possible.
			globals.shuttingDown = true
			// Give server 2 seconds to shut down.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := server.Shutdown(ctx); err != nil {
				// failure/timeout shutting down the server gracefully
				logs.Err.Println("HTTP server failed to terminate gracefully", err)
			}

			// While the server shuts down, termianate all sessions.
			globals.sessionStore.Shutdown()

			// Wait for http server to stop Accept()-ing connections.
			<-httpdone
			cancel()

			// Shutdown local cluster node, if it's a part of a cluster.
			globals.cluster.shutdown()

			// Terminate plugin connections.
			pluginsShutdown()

			// Shutdown gRPC server, if one is configured.
			if globals.grpcServer != nil {
				// GracefulStop does not terminate ServerStream. Must use Stop().
				globals.grpcServer.Stop()
			}

			// Stop publishing statistics.
			statsShutdown()

			// Shutdown the hub. The hub will shutdown topics.
			hubdone := make(chan bool)
			globals.hub.shutdown <- hubdone

			// Wait for the hub to finish.
			<-hubdone

			// Stop updating users cache
			usersShutdown()

			break Loop

		case <-httpdone:
			break Loop
		}
	}
	return nil
}

func signalHandler() <-chan bool {
	stop := make(chan bool)

	signchan := make(chan os.Signal, 1)
	signal.Notify(signchan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		// Wait for a signal. Don't care which signal it is
		sig := <-signchan
		logs.Info.Printf("Signal received: '%s', shutting down", sig)
		stop <- true
	}()

	return stop
}

// Wrapper for http.Handler which optionally adds a Strict-Transport-Security to the response
func hstsHandler(handler http.Handler) http.Handler {
	if globals.tlsStrictMaxAge != "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Strict-Transport-Security", "max-age="+globals.tlsStrictMaxAge)
			handler.ServeHTTP(w, r)
		})
	}
	return handler
}

// The following code is used to intercept HTTP errors so they can be wrapped into json.

// Wrapper around http.ResponseWriter which detects status set to 400+ and replaces
// default error message with a custom one.
type errorResponseWriter struct {
	status int
	http.ResponseWriter
}

func (w *errorResponseWriter) WriteHeader(status int) {
	if status >= http.StatusBadRequest {
		// charset=utf-8 is the default. No need to write it explicitly
		// Must set all the headers before calling super.WriteHeader()
		w.ResponseWriter.Header().Set("Content-Type", "application/json")
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *errorResponseWriter) Write(p []byte) (n int, err error) {
	if w.status >= http.StatusBadRequest {
		p, _ = json.Marshal(
			&ServerComMessage{
				Ctrl: &MsgServerCtrl{
					Timestamp: time.Now().UTC().Round(time.Millisecond),
					Code:      w.status,
					Text:      http.StatusText(w.status),
				},
			})
	}
	return w.ResponseWriter.Write(p)
}

// Handler which deploys errorResponseWriter
func httpErrorHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(&errorResponseWriter{0, w}, r)
		})
}

// Custom 404 response.
func serve404(wrt http.ResponseWriter, req *http.Request) {
	wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
	wrt.WriteHeader(http.StatusNotFound)
	json.NewEncoder(wrt).Encode(
		&ServerComMessage{
			Ctrl: &MsgServerCtrl{
				Timestamp: time.Now().UTC().Round(time.Millisecond),
				Code:      http.StatusNotFound,
				Text:      "not found",
			},
		})
}

// Redirect HTTP requests to HTTPS
func tlsRedirect(toPort string) http.HandlerFunc {
	if toPort == ":443" || toPort == ":https" {
		toPort = ""
	} else if toPort != "" && toPort[:1] == ":" {
		// Strip leading colon. JoinHostPort will add it back.
		toPort = toPort[1:]
	}

	return func(wrt http.ResponseWriter, req *http.Request) {
		host, _, err := net.SplitHostPort(req.Host)
		if err != nil {
			// If SplitHostPort has failed assume it's because :port part is missing.
			host = req.Host
		}

		target, _ := url.ParseRequestURI(req.RequestURI)
		target.Scheme = "https"

		// Ensure valid redirect target.
		if toPort != "" {
			// Replace the port number.
			target.Host = net.JoinHostPort(host, toPort)
		} else {
			target.Host = host
		}

		if target.Path == "" {
			target.Path = "/"
		}

		http.Redirect(wrt, req, target.String(), http.StatusTemporaryRedirect)
	}
}

// Wrapper for http.Handler which optionally adds a Cache-Control header to the response
func cacheControlHandler(maxAge int, handler http.Handler) http.Handler {
	if maxAge > 0 {
		strMaxAge := strconv.Itoa(maxAge)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "must-revalidate, public, max-age="+strMaxAge)
			handler.ServeHTTP(w, r)
		})
	}
	return handler
}

// Get API key from an HTTP request.
func getAPIKey(req *http.Request) string {
	// Check header.
	apikey := req.Header.Get("X-Tinode-APIKey")

	// Check URL query parameters.
	if apikey == "" {
		apikey = req.URL.Query().Get("apikey")
	}

	// Check form values.
	if apikey == "" {
		apikey = req.FormValue("apikey")
	}

	// Check cookies.
	if apikey == "" {
		if c, err := req.Cookie("apikey"); err == nil {
			apikey = c.Value
		}
	}
	return apikey
}

// Extracts authorization credentials from an HTTP request.
// Returns authentication method and secret.
func getHttpAuth(req *http.Request) (method, secret string) {
	// Check X-Tinode-Auth header.
	if parts := strings.Split(req.Header.Get("X-Tinode-Auth"), " "); len(parts) == 2 {
		method, secret = parts[0], parts[1]
		return
	}

	// Check canonical Authorization header.
	if parts := strings.Split(req.Header.Get("Authorization"), " "); len(parts) == 2 {
		method, secret = parts[0], parts[1]
		return
	}

	// Check URL query parameters.
	if method = req.URL.Query().Get("auth"); method != "" {
		secret = req.URL.Query().Get("secret")
		// Convert base64 URL-encoding to standard encoding.
		secret = strings.NewReplacer("-", "+", "_", "/").Replace(secret)
		return
	}

	// Check form values.
	if method = req.FormValue("auth"); method != "" {
		return method, req.FormValue("secret")
	}

	// Check cookies as the last resort.
	if mcookie, err := req.Cookie("auth"); err == nil {
		if scookie, err := req.Cookie("secret"); err == nil {
			method, secret = mcookie.Value, scookie.Value
		}
	}

	return
}

// Authenticate non-websocket HTTP request
func authHttpRequest(req *http.Request) (types.Uid, []byte, error) {
	var uid types.Uid
	if authMethod, secret := getHttpAuth(req); authMethod != "" {
		decodedSecret := make([]byte, base64.StdEncoding.DecodedLen(len(secret)))
		n, err := base64.StdEncoding.Decode(decodedSecret, []byte(secret))
		if err != nil {
			return uid, nil, types.ErrMalformed
		}

		if authhdl := store.Store.GetLogicalAuthHandler(authMethod); authhdl != nil {
			rec, challenge, err := authhdl.Authenticate(decodedSecret[:n], getRemoteAddr(req))
			if err != nil {
				return uid, nil, err
			}
			if challenge != nil {
				return uid, challenge, nil
			}
			uid = rec.Uid
		} else {
			logs.Info.Println("fileUpload: auth data is present but handler is not found", authMethod)
		}
	} else {
		// Find the session, make sure it's appropriately authenticated.
		sess := globals.sessionStore.Get(req.FormValue("sid"))
		if sess != nil {
			uid = sess.uid
		}
	}
	return uid, nil, nil
}

func serveStatus(wrt http.ResponseWriter, req *http.Request) {
	// Disable caching.
	wrt.Header().Set("Cache-Control", "no-cache, no-store, private, max-age=0")
	wrt.Header().Set("Expires", time.Unix(0, 0).Format(http.TimeFormat))
	wrt.Header().Set("Pragma", "no-cache")
	wrt.Header().Set("X-Accel-Expires", "0")

	fmt.Fprintf(wrt, "<html><head>")
	fmt.Fprintf(wrt, "<title>Server Status</title>")
	fmt.Fprintf(wrt, "<style>table { width: 100%%; border: 1px; table-layout: fixed } .tc { display: inline-block; position: relative; width: 100%%; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; vertical-align: top; font-size: 14; } .tc:hover {  z-index: 1; width: auto; max-width: 600px; background-color: #FFFFCC; white-space: normal; } tr:nth-child(even) { background: #f2f2f2 }</style>")
	fmt.Fprintf(wrt, "</head>")

	now := time.Now()
	fmt.Fprintf(wrt, "<body>")
	fmt.Fprintf(wrt, "<h1>%s</h1><div>Version: %s, build: %s. Present time: %s</div>", "Tinode Server", currentVersion, buildstamp, now.Format(time.RFC1123))
	fmt.Fprintf(wrt, "<br><hr>")

	fmt.Fprintf(wrt, "<div>Sessions: %d</div>", len(globals.sessionStore.sessCache))
	fmt.Fprintf(wrt, "<table>")

	fmt.Fprintf(wrt, "<colgroup>")
	fmt.Fprintf(wrt, "<col span='1' style='width: 130px;'>")
	fmt.Fprintf(wrt, "<col span='1' style='width: 200px;'>")
	fmt.Fprintf(wrt, "<col span='1' style='width: 90px;'>")
	fmt.Fprintf(wrt, "<col span='1' style='width: 90px;'>")
	fmt.Fprintf(wrt, "<col span='1' >")
	fmt.Fprintf(wrt, "<col span='1' >")
	fmt.Fprintf(wrt, "</colgroup>")

	fmt.Fprintf(wrt, "<tr><th>remoteAddr</th><th>UA</th><th>uid</th><th>sid</th><th>clnode</th><th>subs</th></tr>")
	globals.sessionStore.Range(func(sid string, s *Session) bool {
		keys := make([]string, 0, len(s.subs))
		for tn := range s.subs {
			keys = append(keys, tn)
		}
		sort.Strings(keys)
		l := strings.Join(keys, ",")
		fmt.Fprintf(wrt, "<tr><td><div class='tc'>%s</div></td><td><div class='tc'>%s</div></td><td><div class='tc'>%s</div></td><td><div class='tc'>%s</div></td><td><div class='tc'>%+v</div></td><td><div class='tc'>%s</div></td></tr>", s.remoteAddr, s.userAgent, s.uid.String(), sid, s.clnode, l)
		return true
	})
	fmt.Fprintf(wrt, "</table>")
	// If this server is a part of a cluster.
	if globals.cluster != nil {
		fmt.Fprintf(wrt, "<div>Cluster node: %s", globals.cluster.thisNodeName)
		fmt.Fprintf(wrt, "</div>")
	}

	fmt.Fprint(wrt, "<br><hr>")
	fmt.Fprintf(wrt, "<div>")
	fmt.Fprintf(wrt, "<div>Topics: %d</div>", globals.hub.numTopics)
	fmt.Fprintf(wrt, "<table>")

	fmt.Fprintf(wrt, "<colgroup>")
	fmt.Fprintf(wrt, "<col span='1' style='width: 190px;'>")
	fmt.Fprintf(wrt, "<col span='1' style='width: 100px;'>")
	fmt.Fprintf(wrt, "<col span='1' style='width: 60px;'>")
	fmt.Fprintf(wrt, "<col span='1' style='width: 30px;'>")
	fmt.Fprintf(wrt, "<col span='1' >")
	fmt.Fprintf(wrt, "<col span='1' >")
	fmt.Fprintf(wrt, "<col span='1' >")
	fmt.Fprintf(wrt, "</colgroup>")

	fmt.Fprintf(wrt, "<tr><th>topicId</th><th>xorig</th><th>isProxy</th><th>cat</th><th>perUser</th><th>perSubs</th><th>sessions</th></tr>")
	globals.hub.topics.Range(func(_, t interface{}) bool {
		topic := t.(*Topic)
		var sd []string
		for s, psd := range topic.sessions {
			sd = append(sd, fmt.Sprintf("%s: %+v", s.sid, psd))
		}
		fmt.Fprintf(wrt, "<tr><td><div class='tc'>%s</div></td><td><div class='tc'>%s</div></td><td><div class='tc'>%t</div></td><td><div class='tc'>%d</div></td><td><div class='tc'>%+v</div></td><td><div class='tc'>%+v</div></td><td><div class='tc'>%s</div></td></tr>", topic.name, topic.xoriginal, topic.isProxy, topic.cat, topic.perUser, topic.perSubs, strings.Join(sd, " "))
		return true
	})
	fmt.Fprintf(wrt, "</div>")

	fmt.Fprintf(wrt, "</body></html>")
}
