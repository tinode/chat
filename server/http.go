/******************************************************************************
 *
 *  Description :
 *
 *  Web server initialization and shutdown.
 *
 *****************************************************************************/

package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"

	"golang.org/x/crypto/acme/autocert"
)

type tlsConfig struct {
	// Flag enabling TLS
	Enabled bool `json:"enabled"`
	// Listen on port 80 and redirect plain HTTP to HTTPS
	RedirectHTTP string `json:"http_redirect"`
	// Enable Strict-Transport-Security by setting max_age > 0
	StrictMaxAge int `json:"strict_max_age"`
	// ACME autocert config, e.g. letsencrypt.org
	Autocert *tlsAutocertConfig `json:"autocert"`
	// If Autocert is not defined, provide file names of static certificate and key
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

type tlsAutocertConfig struct {
	// Domains to support by autocert
	Domains []string `json:"domains"`
	// Name of directory where auto-certificates are cached, e.g. /etc/letsencrypt/live/your-domain-here
	CertCache string `json:"cache"`
	// Contact email for letsencrypt
	Email string `json:"email"`
}

func listenAndServe(addr string, tlsEnabled bool, jsconfig string, stop <-chan bool) error {
	var tlsConfig tlsConfig

	if jsconfig != "" {
		if err := json.Unmarshal([]byte(jsconfig), &tlsConfig); err != nil {
			return errors.New("http: failed to parse tls_config: " + err.Error() + "(" + jsconfig + ")")
		}
	}

	shuttingDown := false

	httpdone := make(chan bool)

	server := &http.Server{Addr: addr}
	if tlsEnabled || tlsConfig.Enabled {

		if tlsConfig.StrictMaxAge > 0 {
			globals.tlsStrictMaxAge = strconv.Itoa(tlsConfig.StrictMaxAge)
		}

		// If port is not specified, use default https port (443),
		// otherwise it will default to 80
		if server.Addr == "" {
			server.Addr = ":https"
		}

		server.TLSConfig = &tls.Config{}
		if tlsConfig.Autocert != nil {
			certManager := autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(tlsConfig.Autocert.Domains...),
				Cache:      autocert.DirCache(tlsConfig.Autocert.CertCache),
				Email:      tlsConfig.Autocert.Email,
			}

			server.TLSConfig.GetCertificate = certManager.GetCertificate
			if tlsConfig.CertFile != "" || tlsConfig.KeyFile != "" {
				log.Printf("HTTP server: using autocert, static cert and key files are ignored")
				tlsConfig.CertFile = ""
				tlsConfig.KeyFile = ""
			}
		} else if tlsConfig.CertFile == "" || tlsConfig.KeyFile == "" {
			return errors.New("HTTP server: missing certificate or key file names")
		}
	}

	go func() {
		var err error
		if tlsEnabled || tlsConfig.Enabled {
			if tlsConfig.RedirectHTTP != "" {
				log.Printf("Redirecting connections from HTTP at [%s] to HTTPS at [%s]",
					tlsConfig.RedirectHTTP, server.Addr)
				go http.ListenAndServe(tlsConfig.RedirectHTTP, tlsRedirect(addr))
			}

			log.Printf("Listening for client HTTPS connections on [%s]", server.Addr)
			err = server.ListenAndServeTLS(tlsConfig.CertFile, tlsConfig.KeyFile)
		} else {
			log.Printf("Listening for client HTTP connections on [%s]", server.Addr)
			err = server.ListenAndServe()
		}
		if err != nil {
			if shuttingDown {
				log.Printf("HTTP server: stopped")
			} else {
				log.Println("HTTP server: failed", err)
			}
		}
		httpdone <- true
	}()

	// Wait for either a termination signal or an error
loop:
	for {
		select {
		case <-stop:
			// Flip the flag that we are terminating and close the Accept-ing socket, so no new connections are possible
			shuttingDown = true
			if err := server.Shutdown(nil); err != nil {
				// failure/timeout shutting down the server gracefully
				return err
			}

			// Wait for http server to stop Accept()-ing connections
			<-httpdone

			// Terminate all sessions
			globals.sessionStore.Shutdown()

			// Shutdown local cluster node, if it's a part of a cluster.
			globals.cluster.shutdown()

			// Shutdown gRPC server, if one is configures
			if globals.grpcServer != nil {
				globals.grpcServer.GracefulStop()
			}

			// Terminate plugin connections
			pluginsShutdown()

			// Shutdown the hub. The hub will shutdown topics
			hubdone := make(chan bool)
			globals.hub.shutdown <- hubdone

			// wait for the hub to finish
			<-hubdone

			break loop

		case <-httpdone:
			break loop
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
		log.Printf("Signal received: '%s', shutting down", sig)
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

func serve404(wrt http.ResponseWriter, req *http.Request) {
	wrt.Header().Set("Content-Type", "application/json; charset=utf-8")
	wrt.WriteHeader(http.StatusNotFound)
	json.NewEncoder(wrt).Encode(
		&ServerComMessage{Ctrl: &MsgServerCtrl{
			Timestamp: time.Now().UTC().Round(time.Millisecond),
			Code:      http.StatusNotFound,
			Text:      "not found"}})
}

// Redirect HTTP requests to HTTPS
func tlsRedirect(toPort string) http.HandlerFunc {
	if toPort == ":443" || toPort == ":https" {
		toPort = ""
	}
	return func(wrt http.ResponseWriter, req *http.Request) {
		target := "https://" + strings.Split(req.Host, ":")[0] + toPort + req.URL.Path
		if req.URL.RawQuery != "" {
			target += "?" + req.URL.RawQuery
		}
		http.Redirect(wrt, req, target, http.StatusTemporaryRedirect)
	}
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

// Get authorization credentials from an HTTP request.
func getHttpAuth(req *http.Request) (string, string) {
	// Check Authorization header.
	if parts := strings.Split(req.Header.Get("Authorization"), " "); len(parts) == 2 {
		return parts[0], parts[1]
	}

	// Check URL query parameters.
	if auth := req.URL.Query().Get("auth"); auth != "" {
		return auth, req.URL.Query().Get("secret")
	}

	// Check form values.
	if auth := req.FormValue("auth"); auth != "" {
		return auth, req.FormValue("secret")
	}

	// Check cookies as the last resort.
	if auth, err := req.Cookie("auth"); err == nil {
		if secret, err := req.Cookie("secret"); err == nil {
			return auth.Value, secret.Value
		}
	}
	return "", ""
}

// Authenticate non-websocket HTTP request
func authHttpRequest(req *http.Request) (types.Uid, error) {
	var uid types.Uid
	if authMethod, secret := getHttpAuth(req); authMethod != "" {
		decodedSecret := make([]byte, base64.StdEncoding.DecodedLen(len(secret)))
		if _, err := base64.StdEncoding.Decode(decodedSecret, []byte(secret)); err != nil {
			return uid, types.ErrMalformed
		}
		authhdl := store.GetAuthHandler(authMethod)
		if authhdl != nil {
			if rec, err := authhdl.Authenticate(decodedSecret); err == nil {
				uid = rec.Uid
			} else {
				return uid, err
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
	return uid, nil
}
