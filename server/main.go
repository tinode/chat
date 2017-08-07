/******************************************************************************
 *
 *  Description :
 *
 *  Setup & initialization.
 *
 *****************************************************************************/

package main

import (
	"encoding/json"
	_ "expvar"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	_ "github.com/tinode/chat/push_fcm"
	_ "github.com/tinode/chat/server/auth_basic"
	_ "github.com/tinode/chat/server/db/rethinkdb"
	"github.com/tinode/chat/server/push"
	_ "github.com/tinode/chat/server/push_stdout"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	// Terminate session after this timeout.
	IDLETIMEOUT = time.Second * 55
	// Keep topic alive after the last session detached.
	TOPICTIMEOUT = time.Second * 5

	// Current API version
	VERSION = "0.13"
	// Minimum supported API version
	MIN_SUPPORTED_VERSION = "0.13"

	// TODO: Move to config
	DEFAULT_GROUP_AUTH_ACCESS = types.ModeCPublic
	DEFAULT_P2P_AUTH_ACCESS   = types.ModeCP2P
	DEFAULT_GROUP_ANON_ACCESS = types.ModeNone
	DEFAULT_P2P_ANON_ACCESS   = types.ModeNone
)

// Build timestamp set by the compiler
var buildstamp = ""

var globals struct {
	hub           *Hub
	sessionStore  *SessionStore
	cluster       *Cluster
	apiKeySalt    []byte
	indexableTags []string
	// Add Strict-Transport-Security to headers, the value signifies age.
	// Empty string "" turns it off
	tlsStrictMaxAge string
}

// Contentx of the configuration file
type configType struct {
	// Default port to listen on. Either numeric or canonical name, e.g. :80 or :https
	// Could be blank: if TLS is not configured, will use port 80, otherwise 443
	// Can be overriden from the command line, see option --listen
	Listen string `json:"listen"`
	// Salt used in signing API keys
	APIKeySalt []byte `json:"api_key_salt"`
	// Tags allowed in index (user discovery)
	IndexableTags []string                   `json:"indexable_tags"`
	ClusterConfig json.RawMessage            `json:"cluster_config"`
	StoreConfig   json.RawMessage            `json:"store_config"`
	PushConfig    json.RawMessage            `json:"push"`
	TlsConfig     json.RawMessage            `json:"tls"`
	AuthConfig    map[string]json.RawMessage `json:"auth_config"`
}

func main() {
	log.Printf("Server v%s:%s pid=%d started with processes: %d", VERSION, buildstamp, os.Getpid(),
		runtime.GOMAXPROCS(runtime.NumCPU()))

	var configfile = flag.String("config", "./tinode.conf", "Path to config file.")
	// Path to static content.
	var staticPath = flag.String("static_data", "", "Path to /static data for the server.")
	var listenOn = flag.String("listen", "", "Override TCP address and port to listen on.")
	var tlsEnabled = flag.Bool("tls_enabled", false, "Override config value for enabling TLS")
	flag.Parse()

	log.Printf("Using config from: '%s'", *configfile)

	var config configType
	if raw, err := ioutil.ReadFile(*configfile); err != nil {
		log.Fatal(err)
	} else if err = json.Unmarshal(raw, &config); err != nil {
		log.Fatal(err)
	}

	if *listenOn != "" {
		config.Listen = *listenOn
	}

	var err = store.Open(string(config.StoreConfig))
	if err != nil {
		log.Fatal("Failed to connect to DB: ", err)
	}
	defer func() {
		store.Close()
		log.Println("Closed database connection(s)")
	}()

	for name, jsconf := range config.AuthConfig {
		if authhdl := store.GetAuthHandler(name); authhdl == nil {
			panic("Config provided for unknown authentication scheme '" + name + "'")
		} else if err := authhdl.Init(string(jsconf)); err != nil {
			panic(err)
		}
	}

	err = push.Init(string(config.PushConfig))
	if err != nil {
		log.Fatal("Failed to initialize push notifications: ", err)
	}
	defer func() {
		push.Stop()
		log.Println("Stopped push notifications")
	}()

	// Keep inactive LP sessions for 15 seconds
	globals.sessionStore = NewSessionStore(IDLETIMEOUT + 15*time.Second)
	// The hub (the main message router)
	globals.hub = newHub()
	// Cluster initialization
	clusterInit(config.ClusterConfig)
	// API key validation secret
	globals.apiKeySalt = config.APIKeySalt
	// Indexable tags for user discovery
	globals.indexableTags = config.IndexableTags

	// Serve static content from the directory in -static_data flag if that's
	// available, otherwise assume '<current dir>/static'.
	var staticContent = *staticPath
	if staticContent == "" {
		path, err := os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
		staticContent = path + "/static/"
	}
	http.Handle("/x/", http.StripPrefix("/x/", hstsHandler(http.FileServer(http.Dir(staticContent)))))
	log.Printf("Serving static content from '%s'", staticContent)

	// Streaming channels
	// Handle websocket clients. WS must come up first, so reconnecting clients won't fall back to LP
	http.HandleFunc("/v0/channels", serveWebSocket)
	// Handle long polling clients
	http.HandleFunc("/v0/channels/lp", serveLongPoll)
	// Serve json-formatted 404 for all other URLs
	http.HandleFunc("/", serve404)

	if err := listenAndServe(config.Listen, *tlsEnabled, string(config.TlsConfig), signalHandler()); err != nil {
		log.Fatal(err)
	}
	log.Println("All done, good bye")
}

func getApiKey(req *http.Request) string {
	apikey := req.FormValue("apikey")
	if apikey == "" {
		apikey = req.Header.Get("X-Tinode-APIKey")
	}
	return apikey
}
