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
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	gh "github.com/gorilla/handlers"

	// For stripping comments from JSON config
	jcr "github.com/tinode/jsonco"

	// Authenticators
	"github.com/tinode/chat/server/auth"
	_ "github.com/tinode/chat/server/auth/anon"
	_ "github.com/tinode/chat/server/auth/basic"
	_ "github.com/tinode/chat/server/auth/rest"
	_ "github.com/tinode/chat/server/auth/token"

	// Database backends
	_ "github.com/tinode/chat/server/db/mongodb"
	_ "github.com/tinode/chat/server/db/mysql"
	_ "github.com/tinode/chat/server/db/rethinkdb"

	// Push notifications
	"github.com/tinode/chat/server/push"
	_ "github.com/tinode/chat/server/push/fcm"
	_ "github.com/tinode/chat/server/push/stdout"
	_ "github.com/tinode/chat/server/push/tnpg"

	"github.com/tinode/chat/server/store"

	// Credential validators
	_ "github.com/tinode/chat/server/validate/email"
	_ "github.com/tinode/chat/server/validate/tel"
	"google.golang.org/grpc"

	// File upload handlers
	_ "github.com/tinode/chat/server/media/fs"
	_ "github.com/tinode/chat/server/media/s3"
)

const (
	// currentVersion is the current API/protocol version
	currentVersion = "0.16"
	// minSupportedVersion is the minimum supported API version
	minSupportedVersion = "0.15"

	// idleSessionTimeout defines duration of being idle before terminating a session.
	idleSessionTimeout = time.Second * 55
	// idleMasterTopicTimeout defines now long to keep master topic alive after the last session detached.
	idleMasterTopicTimeout = time.Second * 4
	// Same as above but shut down the proxy topic sooner. Otherwise master topic would be kept alive for too long.
	idleProxyTopicTimeout = time.Second * 2

	// defaultMaxMessageSize is the default maximum message size
	defaultMaxMessageSize = 1 << 19 // 512K

	// defaultMaxSubscriberCount is the default maximum number of group topic subscribers.
	// Also set in adapter.
	defaultMaxSubscriberCount = 256

	// defaultMaxTagCount is the default maximum number of indexable tags
	defaultMaxTagCount = 16

	// minTagLength is the shortest acceptable length of a tag in runes. Shorter tags are discarded.
	minTagLength = 2
	// maxTagLength is the maximum length of a tag in runes. Longer tags are trimmed.
	maxTagLength = 96

	// Delay before updating a User Agent
	uaTimerDelay = time.Second * 5

	// maxDeleteCount is the maximum allowed number of messages to delete in one call.
	defaultMaxDeleteCount = 1024

	// Base URL path for serving the streaming API.
	defaultApiPath = "/"

	// Mount point where static content is served, http://host-name<defaultStaticMount>
	defaultStaticMount = "/"

	// Local path to static content
	defaultStaticPath = "static"

	// Default country code to fall back to if the "default_country_code" field
	// isn't specified in the config.
	defaultCountryCode = "US"
)

// Build version number defined by the compiler:
// 		-ldflags "-X main.buildstamp=value_to_assign_to_buildstamp"
// Reported to clients in response to {hi} message.
// For instance, to define the buildstamp as a timestamp of when the server was built add a
// flag to compiler command line:
// 		-ldflags "-X main.buildstamp=`date -u '+%Y%m%dT%H:%M:%SZ'`"
// or to set it to git tag:
// 		-ldflags "-X main.buildstamp=`git describe --tags`"
var buildstamp = "undef"

// CredValidator holds additional config params for a credential validator.
type credValidator struct {
	// AuthLevel(s) which require this validator.
	requiredAuthLvl []auth.Level
	addToTags       bool
}

var globals struct {
	// Topics cache and processing.
	hub *Hub
	// Indicator that shutdown is in progress
	shuttingDown bool
	// Sessions cache.
	sessionStore *SessionStore
	// Cluster data.
	cluster *Cluster
	// gRPC server.
	grpcServer *grpc.Server
	// Plugins.
	plugins []Plugin
	// Runtime statistics communication channel.
	statsUpdate chan *varUpdate
	// Users cache communication channel.
	usersUpdate chan *UserCacheReq

	// Credential validators.
	validators map[string]credValidator
	// Validators required for each auth level.
	authValidators map[auth.Level][]string

	// Salt used for signing API key.
	apiKeySalt []byte
	// Tag namespaces (prefixes) which are immutable to the client.
	immutableTagNS map[string]bool
	// Tag namespaces which are immutable on User and partially mutable on Topic:
	// user can only mutate tags he owns.
	maskedTagNS map[string]bool

	// Add Strict-Transport-Security to headers, the value signifies age.
	// Empty string "" turns it off
	tlsStrictMaxAge string
	// Listen for connections on this address:port and redirect them to HTTPS port.
	tlsRedirectHTTP string
	// Maximum message size allowed from peer.
	maxMessageSize int64
	// Maximum number of group topic subscribers.
	maxSubscriberCount int
	// Maximum number of indexable tags.
	maxTagCount int

	// Maximum allowed upload size.
	maxFileUploadSize int64

	// Prioritise X-Forwarded-For header as the source of IP address of the client.
	useXForwardedFor bool

	// Country code to assign to sessions by default.
	defaultCountryCode string
}

type validatorConfig struct {
	// TRUE or FALSE to set
	AddToTags bool `json:"add_to_tags"`
	//  Authentication level which triggers this validator: "auth", "anon"... or ""
	Required []string `json:"required"`
	// Validator params passed to validator unchanged.
	Config json.RawMessage `json:"config"`
}

type mediaConfig struct {
	// The name of the handler to use for file uploads.
	UseHandler string `json:"use_handler"`
	// Maximum allowed size of an uploaded file
	MaxFileUploadSize int64 `json:"max_size"`
	// Garbage collection timeout
	GcPeriod int `json:"gc_period"`
	// Number of entries to delete in one pass
	GcBlockSize int `json:"gc_block_size"`
	// Individual handler config params to pass to handlers unchanged.
	Handlers map[string]json.RawMessage `json:"handlers"`
}

// Contentx of the configuration file
type configType struct {
	// HTTP(S) address:port to listen on for websocket and long polling clients. Either a
	// numeric or a canonical name, e.g. ":80" or ":https". Could include a host name, e.g.
	// "localhost:80".
	// Could be blank: if TLS is not configured, will use ":80", otherwise ":443".
	// Can be overridden from the command line, see option --listen.
	Listen string `json:"listen"`
	// Base URL path where the streaming and large file API calls are served, default is '/'.
	// Can be overridden from the command line, see option --api_path.
	ApiPath string `json:"api_path"`
	// Cache-Control value for static content.
	CacheControl int `json:"cache_control"`
	// Address:port to listen for gRPC clients. If blank gRPC support will not be initialized.
	// Could be overridden from the command line with --grpc_listen.
	GrpcListen string `json:"grpc_listen"`
	// Enable handling of gRPC keepalives https://github.com/grpc/grpc/blob/master/doc/keepalive.md
	// This sets server's GRPC_ARG_KEEPALIVE_TIME_MS to 60 seconds instead of the default 2 hours.
	GrpcKeepalive bool `json:"grpc_keepalive_enabled"`
	// URL path for mounting the directory with static files (usually TinodeWeb).
	StaticMount string `json:"static_mount"`
	// Local path to static files. All files in this path are made accessible by HTTP.
	StaticData string `json:"static_data"`
	// Salt used in signing API keys
	APIKeySalt []byte `json:"api_key_salt"`
	// Maximum message size allowed from client. Intended to prevent malicious client from sending
	// very large files inband (does not affect out of band uploads).
	MaxMessageSize int `json:"max_message_size"`
	// Maximum number of group topic subscribers.
	MaxSubscriberCount int `json:"max_subscriber_count"`
	// Masked tags: tags immutable on User (mask), mutable on Topic only within the mask.
	MaskedTagNamespaces []string `json:"masked_tags"`
	// Maximum number of indexable tags
	MaxTagCount int `json:"max_tag_count"`
	// URL path for exposing runtime stats. Disabled if the path is blank.
	ExpvarPath string `json:"expvar"`
	// Take IP address of the client from HTTP header 'X-Forwarded-For'.
	// Useful when tinode is behind a proxy. If missing, fallback to default RemoteAddr.
	UseXForwardedFor bool `json:"use_x_forwarded_for"`
	// 2-letter country code to assign to sessions by default when the country isn't specified
	// by the client explicitly and it's impossible to infer it.
	DefaultCountryCode string `json:"default_country_code"`

	// Configs for subsystems
	Cluster   json.RawMessage             `json:"cluster_config"`
	Plugin    json.RawMessage             `json:"plugins"`
	Store     json.RawMessage             `json:"store_config"`
	Push      json.RawMessage             `json:"push"`
	TLS       json.RawMessage             `json:"tls"`
	Auth      map[string]json.RawMessage  `json:"auth_config"`
	Validator map[string]*validatorConfig `json:"acc_validation"`
	Media     *mediaConfig                `json:"media"`
}

func main() {
	executable, _ := os.Executable()

	// All relative paths are resolved against the executable path, not against current working directory.
	// Absolute paths are left unchanged.
	rootpath, _ := filepath.Split(executable)

	log.Printf("Server v%s:%s:%s; pid %d; %d process(es)",
		currentVersion, executable, buildstamp,
		os.Getpid(), runtime.GOMAXPROCS(runtime.NumCPU()))

	var configfile = flag.String("config", "tinode.conf", "Path to config file.")
	// Path to static content.
	var staticPath = flag.String("static_data", defaultStaticPath, "File path to directory with static files to be served.")
	var listenOn = flag.String("listen", "", "Override address and port to listen on for HTTP(S) clients.")
	var apiPath = flag.String("api_path", "", "Override the base URL path where API is served.")
	var listenGrpc = flag.String("grpc_listen", "", "Override address and port to listen on for gRPC clients.")
	var tlsEnabled = flag.Bool("tls_enabled", false, "Override config value for enabling TLS.")
	var clusterSelf = flag.String("cluster_self", "", "Override the name of the current cluster node.")
	var expvarPath = flag.String("expvar", "", "Override the URL path where runtime stats are exposed. Use '-' to disable.")
	var pprofFile = flag.String("pprof", "", "File name to save profiling info to. Disabled if not set.")
	var pprofUrl = flag.String("pprof_url", "", "Debugging only! URL path for exposing profiling info. Disabled if not set.")
	flag.Parse()

	*configfile = toAbsolutePath(rootpath, *configfile)
	log.Printf("Using config from '%s'", *configfile)

	var config configType
	if file, err := os.Open(*configfile); err != nil {
		log.Fatal("Failed to read config file: ", err)
	} else {
		jr := jcr.New(file)
		if err = json.NewDecoder(jr).Decode(&config); err != nil {
			switch jerr := err.(type) {
			case *json.UnmarshalTypeError:
				lnum, cnum, _ := jr.LineAndChar(jerr.Offset)
				log.Fatalf("Unmarshall error in config file in %s at %d:%d (offset %d bytes): %s",
					jerr.Field, lnum, cnum, jerr.Offset, jerr.Error())
			case *json.SyntaxError:
				lnum, cnum, _ := jr.LineAndChar(jerr.Offset)
				log.Fatalf("Syntax error in config file at %d:%d (offset %d bytes): %s",
					lnum, cnum, jerr.Offset, jerr.Error())
			default:
				log.Fatal("Failed to parse config file: ", err)
			}
		}
		file.Close()
	}

	if *listenOn != "" {
		config.Listen = *listenOn
	}

	// Set up HTTP server. Must use non-default mux because of expvar.
	mux := http.NewServeMux()

	// Exposing values for statistics and monitoring.
	evpath := *expvarPath
	if evpath == "" {
		evpath = config.ExpvarPath
	}
	statsInit(mux, evpath)
	statsRegisterInt("Version")
	statsSet("Version", base10Version(parseVersion(currentVersion)))

	// Initialize serving debug profiles (optional).
	servePprof(mux, *pprofUrl)

	// Initialize cluster and receive calculated workerId.
	// Cluster won't be started here yet.
	workerId := clusterInit(config.Cluster, clusterSelf)

	if *pprofFile != "" {
		*pprofFile = toAbsolutePath(rootpath, *pprofFile)

		cpuf, err := os.Create(*pprofFile + ".cpu")
		if err != nil {
			log.Fatal("Failed to create CPU pprof file: ", err)
		}
		defer cpuf.Close()

		memf, err := os.Create(*pprofFile + ".mem")
		if err != nil {
			log.Fatal("Failed to create Mem pprof file: ", err)
		}
		defer memf.Close()

		pprof.StartCPUProfile(cpuf)
		defer pprof.StopCPUProfile()
		defer pprof.WriteHeapProfile(memf)

		log.Printf("Profiling info saved to '%s.(cpu|mem)'", *pprofFile)
	}

	err := store.Open(workerId, config.Store)
	if err != nil {
		log.Fatal("Failed to connect to DB: ", err)
	}
	log.Println("DB adapter", store.GetAdapterName())
	defer func() {
		store.Close()
		log.Println("Closed database connection(s)")
		log.Println("All done, good bye")
	}()

	// API key signing secret
	globals.apiKeySalt = config.APIKeySalt

	err = store.InitAuthLogicalNames(config.Auth["logical_names"])
	if err != nil {
		log.Fatal(err)
	}

	// List of tag namespaces for user discovery which cannot be changed directly
	// by the client, e.g. 'email' or 'tel'.
	globals.immutableTagNS = make(map[string]bool)

	authNames := store.GetAuthNames()
	for _, name := range authNames {
		if authhdl := store.GetLogicalAuthHandler(name); authhdl == nil {
			log.Fatalln("Unknown authenticator", name)
		} else if jsconf := config.Auth[name]; jsconf != nil {
			if err := authhdl.Init(jsconf, name); err != nil {
				log.Fatalln("Failed to init auth scheme", name+":", err)
			}
			tags, err := authhdl.RestrictedTags()
			if err != nil {
				log.Fatalln("Failed get restricted tag namespaces", name+":", err)
			}
			for _, tag := range tags {
				globals.immutableTagNS[tag] = true
			}
		}
	}

	// Process validators.
	for name, vconf := range config.Validator {
		// Check if validator is restrictive. If so, add validator name to the list of restricted tags.
		// The namespace can be restricted even if the validator is disabled.
		if vconf.AddToTags {
			if strings.Contains(name, ":") {
				log.Fatal("acc_validation names should not contain character ':'")
			}
			globals.immutableTagNS[name] = true
		}

		if len(vconf.Required) == 0 {
			// Skip disabled validator.
			continue
		}

		var reqLevels []auth.Level
		for _, req := range vconf.Required {
			lvl := auth.ParseAuthLevel(req)
			if lvl == auth.LevelNone {
				if req != "" {
					log.Fatalf("Invalid required AuthLevel '%s' in validator '%s'", req, name)
				}
				// Skip empty string
				continue
			}
			reqLevels = append(reqLevels, lvl)
			if globals.authValidators == nil {
				globals.authValidators = make(map[auth.Level][]string)
			}
			globals.authValidators[lvl] = append(globals.authValidators[lvl], name)
		}

		if len(reqLevels) == 0 {
			// Ignore validator with empty levels.
			continue
		}

		if val := store.GetValidator(name); val == nil {
			log.Fatal("Config provided for an unknown validator '" + name + "'")
		} else if err = val.Init(string(vconf.Config)); err != nil {
			log.Fatal("Failed to init validator '"+name+"': ", err)
		}
		if globals.validators == nil {
			globals.validators = make(map[string]credValidator)
		}
		globals.validators[name] = credValidator{
			requiredAuthLvl: reqLevels,
			addToTags:       vconf.AddToTags}
	}

	// Partially restricted tag namespaces
	globals.maskedTagNS = make(map[string]bool, len(config.MaskedTagNamespaces))
	for _, tag := range config.MaskedTagNamespaces {
		if strings.Contains(tag, ":") {
			log.Fatal("masked_tags namespaces should not contain character ':'")
		}
		globals.maskedTagNS[tag] = true
	}

	var tags []string
	for tag := range globals.immutableTagNS {
		tags = append(tags, "'"+tag+"'")
	}
	if len(tags) > 0 {
		log.Println("Restricted tags:", tags)
	}
	tags = nil
	for tag := range globals.maskedTagNS {
		tags = append(tags, "'"+tag+"'")
	}
	if len(tags) > 0 {
		log.Println("Masked tags:", tags)
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

	globals.useXForwardedFor = config.UseXForwardedFor
	globals.defaultCountryCode = config.DefaultCountryCode
	if globals.defaultCountryCode == "" {
		globals.defaultCountryCode = defaultCountryCode
	}

	if config.Media != nil {
		if config.Media.UseHandler == "" {
			config.Media = nil
		} else {
			globals.maxFileUploadSize = config.Media.MaxFileUploadSize
			if config.Media.Handlers != nil {
				var conf string
				if params := config.Media.Handlers[config.Media.UseHandler]; params != nil {
					conf = string(params)
				}
				if err = store.UseMediaHandler(config.Media.UseHandler, conf); err != nil {
					log.Fatalf("Failed to init media handler '%s': %s", config.Media.UseHandler, err)
				}
			}
			if config.Media.GcPeriod > 0 && config.Media.GcBlockSize > 0 {
				stopFilesGc := largeFileRunGarbageCollection(time.Second*time.Duration(config.Media.GcPeriod),
					config.Media.GcBlockSize)
				defer func() {
					stopFilesGc <- true
					log.Println("Stopped files garbage collector")
				}()
			}
		}
	}

	err = push.Init(string(config.Push))
	if err != nil {
		log.Fatal("Failed to initialize push notifications:", err)
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

	tlsConfig, err := parseTLSConfig(*tlsEnabled, config.TLS)
	if err != nil {
		log.Fatalln(err)
	}

	// Intialize plugins
	pluginsInit(config.Plugin)

	// Initialize users cache
	usersInit()

	// Set up gRPC server, if one is configured
	if *listenGrpc == "" {
		*listenGrpc = config.GrpcListen
	}
	if globals.grpcServer, err = serveGrpc(*listenGrpc, config.GrpcKeepalive, tlsConfig); err != nil {
		log.Fatal(err)
	}

	// Serve static content from the directory in -static_data flag if that's
	// available, otherwise assume '<path-to-executable>/static'. The content is served at
	// the path pointed by 'static_mount' in the config. If that is missing then it's
	// served at root '/'.
	var staticMountPoint string
	if *staticPath != "" && *staticPath != "-" {
		// Resolve path to static content.
		*staticPath = toAbsolutePath(rootpath, *staticPath)
		if _, err = os.Stat(*staticPath); os.IsNotExist(err) {
			log.Fatal("Static content directory is not found", *staticPath)
		}

		staticMountPoint = config.StaticMount
		if staticMountPoint == "" {
			staticMountPoint = defaultStaticMount
		} else {
			if !strings.HasPrefix(staticMountPoint, "/") {
				staticMountPoint = "/" + staticMountPoint
			}
			if !strings.HasSuffix(staticMountPoint, "/") {
				staticMountPoint += "/"
			}
		}
		mux.Handle(staticMountPoint,
			// Add optional Cache-Control header
			cacheControlHandler(config.CacheControl,
				// Optionally add Strict-Transport_security to the response
				hstsHandler(
					// Add gzip compression
					gh.CompressHandler(
						// And add custom formatter of errors.
						httpErrorHandler(
							// Remove mount point prefix
							http.StripPrefix(staticMountPoint,
								http.FileServer(http.Dir(*staticPath))))))))
		log.Printf("Serving static content from '%s' at '%s'", *staticPath, staticMountPoint)
	} else {
		log.Println("Static content is disabled")
	}

	// Configure root path for serving API calls.
	if *apiPath != "" {
		config.ApiPath = *apiPath
	}
	if config.ApiPath == "" {
		config.ApiPath = defaultApiPath
	} else {
		if !strings.HasPrefix(config.ApiPath, "/") {
			config.ApiPath = "/" + config.ApiPath
		}
		if !strings.HasSuffix(config.ApiPath, "/") {
			config.ApiPath += "/"
		}
	}
	log.Printf("API served from root URL path '%s'", config.ApiPath)

	// Handle websocket clients.
	mux.HandleFunc(config.ApiPath+"v0/channels", serveWebSocket)
	// Handle long polling clients. Enable compression.
	mux.Handle(config.ApiPath+"v0/channels/lp", gh.CompressHandler(http.HandlerFunc(serveLongPoll)))
	if config.Media != nil {
		// Handle uploads of large files.
		mux.Handle(config.ApiPath+"v0/file/u/", gh.CompressHandler(http.HandlerFunc(largeFileUpload)))
		// Serve large files.
		mux.Handle(config.ApiPath+"v0/file/s/", gh.CompressHandler(http.HandlerFunc(largeFileServe)))
		log.Println("Large media handling enabled", config.Media.UseHandler)
	}

	if staticMountPoint != "/" {
		// Serve json-formatted 404 for all other URLs
		mux.HandleFunc("/", serve404)
	}

	if err = listenAndServe(config.Listen, mux, tlsConfig, signalHandler()); err != nil {
		log.Fatal(err)
	}
}
