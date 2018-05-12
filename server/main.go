/******************************************************************************
 *
 *  Description :
 *
 *  Setup & initialization.
 *
 *****************************************************************************/

package main

//go:generate protoc --proto_path=../pbx --go_out=plugins=grpc:../pbx ../pbx/model.proto

import (
	"encoding/json"
	_ "expvar"
	"flag"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	// For stripping comments from JSON config
	jcr "github.com/DisposaBoy/JsonConfigReader"
	gzip "github.com/gorilla/handlers"

	// Authenticators
	"github.com/tinode/chat/server/auth"
	_ "github.com/tinode/chat/server/auth/anon"
	_ "github.com/tinode/chat/server/auth/basic"
	_ "github.com/tinode/chat/server/auth/token"

	// Database backends
	_ "github.com/tinode/chat/server/db/mysql"
	_ "github.com/tinode/chat/server/db/rethinkdb"

	// Push notifications
	"github.com/tinode/chat/server/push"
	_ "github.com/tinode/chat/server/push/fcm"
	_ "github.com/tinode/chat/server/push/stdout"

	"github.com/tinode/chat/server/store"

	// Credential validators
	_ "github.com/tinode/chat/server/validate/email"
	_ "github.com/tinode/chat/server/validate/tel"
	"google.golang.org/grpc"
)

const (
	// idleSessionTimeout defines duration of being idle before terminating a session.
	idleSessionTimeout = time.Second * 55
	// idleTopicTimeout defines now long to keep topic alive after the last session detached.
	idleTopicTimeout = time.Second * 5

	// currentVersion is the current API/protocol version
	currentVersion = "0.14"
	// minSupportedVersion is the minimum supported API version
	minSupportedVersion = "0.14"

	// defaultMaxMessageSize is the default maximum message size
	defaultMaxMessageSize = 1 << 19 // 512K

	// defaultMaxSubscriberCount is the default maximum number of group topic subscribers.
	// Also set in adapter.
	defaultMaxSubscriberCount = 256

	// defaultMaxTagCount is the default maximum number of indexable tags
	defaultMaxTagCount = 16

	// minTagLength is the shortest acceptable length of a tag in runes. Shorter tags are discarded.
	minTagLength = 4
	// maxTagLength is the maximum length of a tag in runes. Longer tags are trimmed.
	maxTagLength = 96

	// Delay before updating a User Agent
	uaTimerDelay = time.Second * 5

	// maxDeleteCount is the maximum allowed number of messages to delete in one call.
	defaultMaxDeleteCount = 1024

	// Mount point where static content is served, http://host-name/<defaultStaticMount>
	defaultStaticMount = "/x/"

	// Local path to static content
	defaultStaticPath = "/static/"
)

// Build version number defined by the compiler:
// 		-ldflags "-X main.buildstamp=value_to_assign_to_buildstamp"
// Reported to clients in response to {hi} message.
// For instance, to define the buildstamp as a timestamp of when the server was built add a
// flag to compiler command line:
// 		-ldflags "-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'`"
var buildstamp = "undef"

// CredValidator holds additional config params for a credential validator.
type credValidator struct {
	// AuthLevel(s) which require this validator.
	requiredAuthLvl []auth.Level
	addToTags       bool
}

var globals struct {
	hub          *Hub
	sessionStore *SessionStore
	cluster      *Cluster
	grpcServer   *grpc.Server
	plugins      []Plugin
	// Credential validators.
	validators map[string]credValidator
	// Validators required for each auth level.
	authValidators map[auth.Level][]string

	apiKeySalt []byte
	// Tag namespaces (prefixes) which are immutable to the client.
	immutableTagNS map[string]bool
	// Tag namespaces which are immutable on User and partially mutable on Topic:
	// user can only mutate tags he owns.
	maskedTagNS map[string]bool

	// Add Strict-Transport-Security to headers, the value signifies age.
	// Empty string "" turns it off
	tlsStrictMaxAge string
	// Maximum message size allowed from peer.
	maxMessageSize int64
	// Maximum number of group topic subscribers.
	maxSubscriberCount int
	// Maximum number of indexable tags.
	maxTagCount int
}

type validatorConfig struct {
	// TRUE or FALSE to set
	AddToTags bool `json:"add_to_tags"`
	//  Authentication level which triggers this validator: "auth", "anon"... or ""
	Required []string `json:"required"`
	// Validator params passed to validator unchanged.
	Config json.RawMessage `json:"config"`
}

// Contentx of the configuration file
type configType struct {
	// Default HTTP(S) address:port to listen on for websocket and long polling clients. Either a
	// numeric or a canonical name, e.g. ":80" or ":https". Could include a host name, e.g.
	// "localhost:80".
	// Could be blank: if TLS is not configured, will use ":80", otherwise ":443".
	// Can be overridden from the command line, see option --listen.
	Listen string `json:"listen"`
	// Address:port to listen for gRPC clients. If blank gRPC support will not be initialized.
	// Could be overridden from the command line with --grpc_listen.
	GrpcListen string `json:"grpc_listen"`
	// URL path for mounting the directory with static files.
	StaticMount string `json:"static_mount"`
	// Local path to static files. All files in this path are made accessible by HTTP.
	StaticData string `json:"static_data"`
	// Salt used in signing API keys
	APIKeySalt []byte `json:"api_key_salt"`
	// Maximum message size allowed from client. Intended to prevent malicious client from sending
	// very large files.
	MaxMessageSize int `json:"max_message_size"`
	// Maximum number of group topic subscribers.
	MaxSubscriberCount int `json:"max_subscriber_count"`
	// Masked tags: tags immutable on User (mask), mutable on Topic only within the mask.
	MaskedTagNamespaces []string `json:"masked_tags"`
	// Maximum number of indexable tags
	MaxTagCount int `json:"max_tag_count"`

	// Configs for subsystems
	Cluster json.RawMessage `json:"cluster_config"`

	Plugin    json.RawMessage             `json:"plugins"`
	Store     json.RawMessage             `json:"store_config"`
	Push      json.RawMessage             `json:"push"`
	TLS       json.RawMessage             `json:"tls"`
	Auth      map[string]json.RawMessage  `json:"auth_config"`
	Validator map[string]*validatorConfig `json:"acc_validation"`
}

func main() {
	log.Printf("Server 'v%s:%s:%s'; pid %d; started with %d process(es)", currentVersion,
		buildstamp, store.GetAdapterName(), os.Getpid(), runtime.GOMAXPROCS(runtime.NumCPU()))

	var configfile = flag.String("config", "./tinode.conf", "Path to config file.")
	// Path to static content.
	var staticPath = flag.String("static_data", "", "Path to /static data for the server.")
	var listenOn = flag.String("listen", "", "Override address and port to listen on for HTTP(S) clients.")
	var listenGrpc = flag.String("grpc_listen", "", "Override address and port to listen on for gRPC clients.")
	var tlsEnabled = flag.Bool("tls_enabled", false, "Override config value for enabling TLS")
	var clusterSelf = flag.String("cluster_self", "", "Override the name of the current cluster node")
	flag.Parse()

	log.Printf("Using config from '%s'", *configfile)

	var config configType
	if file, err := os.Open(*configfile); err != nil {
		log.Fatal("Failed to read config file:", err)
	} else if err = json.NewDecoder(jcr.New(file)).Decode(&config); err != nil {
		log.Fatal("Failed to parse config file:", err)
	}

	if *listenOn != "" {
		config.Listen = *listenOn
	}

	// Initialize cluster and receive calculated workerId.
	// Cluster won't be started here yet.
	workerId := clusterInit(config.Cluster, clusterSelf)

	var err = store.Open(workerId, string(config.Store))
	if err != nil {
		log.Fatal("Failed to connect to DB:", err)
	}
	defer func() {
		store.Close()
		log.Println("Closed database connection(s)")
		log.Println("All done, good bye")
	}()

	// API key signing secret
	globals.apiKeySalt = config.APIKeySalt

	for name, jsconf := range config.Auth {
		if authhdl := store.GetAuthHandler(name); authhdl == nil {
			panic("Config provided for unknown authentication scheme '" + name + "'")
		} else if err := authhdl.Init(string(jsconf)); err != nil {
			panic(err)
		}
	}

	for name, vconf := range config.Validator {
		if len(vconf.Required) == 0 {
			// Skip disabled validator.
			continue
		}
		var reqLevels []auth.Level
		for _, req := range vconf.Required {
			lvl := auth.ParseAuthLevel(req)
			if lvl == auth.LevelNone {
				// Skip unknown authentication level.
				continue
			}
			reqLevels = append(reqLevels, lvl)
			if globals.authValidators == nil {
				globals.authValidators = make(map[auth.Level][]string)
			}
			globals.authValidators[lvl] = append(globals.authValidators[lvl], name)
		}

		if val := store.GetValidator(name); val == nil {
			panic("Config provided for an unknown validator '" + name + "'")
		} else if err := val.Init(string(vconf.Config)); err != nil {
			panic(err)
		}
		if globals.validators == nil {
			globals.validators = make(map[string]credValidator)
		}
		globals.validators[name] = credValidator{
			requiredAuthLvl: reqLevels,
			addToTags:       vconf.AddToTags}
	}

	// List of tag namespaces for user discovery which cannot be changed directly
	// by the client, e.g. 'email' or 'tel'.
	globals.immutableTagNS = make(map[string]bool, len(config.Validator))
	for tag, _ := range config.Validator {
		if strings.Index(tag, ":") >= 0 {
			panic("acc_validation names should not contain character ':'")
		}
		globals.immutableTagNS[tag] = true
	}

	// Partially restricted tag namespaces
	globals.maskedTagNS = make(map[string]bool, len(config.MaskedTagNamespaces))
	for _, tag := range config.MaskedTagNamespaces {
		if strings.Index(tag, ":") >= 0 {
			panic("masked_tags namespaces should not contain character ':'")
		}
		globals.maskedTagNS[tag] = true
	}

	// Maximum message size
	globals.maxMessageSize = int64(config.MaxMessageSize)
	if globals.maxMessageSize <= 0 {
		globals.maxMessageSize = defaultMaxMessageSize
	}
	// Maximum number of group topic subscribers
	globals.maxSubscriberCount = config.MaxSubscriberCount
	if globals.maxSubscriberCount <= 1 {
		globals.maxSubscriberCount = defaultMaxSubscriberCount
	}
	// Maximum number of indexable tags per user or topics
	globals.maxTagCount = config.MaxTagCount
	if globals.maxTagCount <= 0 {
		globals.maxTagCount = defaultMaxTagCount
	}

	err = push.Init(string(config.Push))
	if err != nil {
		log.Fatal("Failed to initialize push notifications: ", err)
	}
	defer func() {
		push.Stop()
		log.Println("Stopped push notifications")
	}()

	// Keep inactive LP sessions for 15 seconds
	globals.sessionStore = NewSessionStore(idleSessionTimeout + 15*time.Second)
	// The hub (the main message router)
	globals.hub = newHub()

	// Start accepting cluster traffic.
	if globals.cluster != nil {
		globals.cluster.start()
	}

	// Intialize plugins
	pluginsInit(config.Plugin)

	// Serve static content from the directory in -static_data flag if that's
	// available, otherwise assume '<current dir>/static'. The content is served at
	// the path pointed by 'static_mount' in the config. If that is missing then it's
	// served at '/x/'.
	var staticContent = *staticPath
	if staticContent == "" {
		path, err := os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
		staticContent = path + defaultStaticPath
	}
	staticMountPoint := config.StaticMount
	if staticMountPoint == "" {
		staticMountPoint = defaultStaticMount
	} else {
		if !strings.HasPrefix(staticMountPoint, "/") {
			staticMountPoint = "/" + staticMountPoint
		}
		if !strings.HasSuffix(staticMountPoint, "/") {
			staticMountPoint = staticMountPoint + "/"
		}
	}
	http.Handle(staticMountPoint,
		// Add gzip compression
		gzip.CompressHandler(
			// Remove mount point prefix
			http.StripPrefix(staticMountPoint,
				// Optionally add Strict-Transport_security to the response
				hstsHandler(http.FileServer(http.Dir(staticContent))))))
	log.Printf("Serving static content from '%s' at '%s'", staticContent, staticMountPoint)

	// Configure HTTP channels
	// Handle websocket clients.
	http.HandleFunc("/v0/channels", serveWebSocket)
	// Handle long polling clients. Enable compression.
	http.Handle("/v0/channels/lp", gzip.CompressHandler(http.HandlerFunc(serveLongPoll)))
	// Serve json-formatted 404 for all other URLs
	http.HandleFunc("/", serve404)

	// Set up gRPC server, if one is configured
	if *listenGrpc == "" {
		*listenGrpc = config.GrpcListen
	}
	if srv, err := serveGrpc(*listenGrpc); err != nil {
		log.Fatal(err)
	} else {
		globals.grpcServer = srv
	}

	if err := listenAndServe(config.Listen, *tlsEnabled, string(config.TLS), signalHandler()); err != nil {
		log.Fatal(err)
	}
}

func getAPIKey(req *http.Request) string {
	apikey := req.FormValue("apikey")
	if apikey == "" {
		apikey = req.Header.Get("X-Tinode-APIKey")
	}
	return apikey
}
