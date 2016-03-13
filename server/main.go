/******************************************************************************
 *
 *  Copyright (C) 2014 Tinode, All Rights Reserved
 *
 *  This program is free software; you can redistribute it and/or modify it
 *  under the terms of the GNU Affero General Public License as published by
 *  the Free Software Foundation; either version 3 of the License, or (at your
 *  option) any later version.
 *
 *  This program is distributed in the hope that it will be useful, but
 *  WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY
 *  or FITNESS FOR A PARTICULAR PURPOSE.
 *  See the GNU Affero General Public License for more details.
 *
 *  You should have received a copy of the GNU Affero General Public License
 *  along with this program; if not, see <http://www.gnu.org/licenses>.
 *
 *  This code is available under licenses for commercial use.
 *
 *  File        :  main.go
 *  Author      :  Gene Sokolov
 *  Created     :  18-May-2014
 *
 ******************************************************************************
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

	_ "github.com/tinode/chat/server/auth_basic"
	_ "github.com/tinode/chat/server/db/rethinkdb"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

const (
	// Terminate session after this timeout.
	IDLETIMEOUT = time.Second * 55
	// Keep topic alive after the last session detached.
	TOPICTIMEOUT = time.Second * 5

	// API version
	VERSION = "0.6"

	DEFAULT_AUTH_ACCESS = types.ModePublic
	DEFAULT_ANON_ACCESS = types.ModeNone
)

// Build timestamp set by the compiler
var buildstamp = ""

var globals struct {
	hub            *Hub
	sessionStore   *SessionStore
	apiKeySalt     []byte
	tokenExpiresIn time.Duration
}

type configType struct {
	Listen     string `json:"listen"`
	Adapter    string `json:"use_adapter"`
	APIKeySalt []byte `json:"api_key_salt"`
	// Security token expiration time
	TokenExpiresIn int             `json:"token_expires_in"`
	StoreConfig    json.RawMessage `json:"store_config"`
}

func main() {
	log.Printf("Server pid=%d started with processes: %d", os.Getpid(), runtime.GOMAXPROCS(runtime.NumCPU()))

	var configfile = flag.String("config", "./tinode.conf", "Path to config file.")
	// Path to static content.
	var staticPath = flag.String("static_data", "", "Path to /static data for the server.")
	var listenOn = flag.String("listen", "", "Override tinode.conf address and port to listen on.")
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

	var err = store.Open(config.Adapter, string(config.StoreConfig))
	if err != nil {
		log.Fatal("Failed to connect to DB: ", err)
	}
	defer func() {
		store.Close()
		log.Println("Closed database connection(s)")
	}()

	// Keep inactive LP sessions for 70 seconds
	globals.sessionStore = NewSessionStore(IDLETIMEOUT + 15*time.Second)
	globals.hub = newHub()
	globals.tokenExpiresIn = time.Duration(config.TokenExpiresIn) * time.Second
	globals.apiKeySalt = config.APIKeySalt

	// Serve static content from the directory in -static_data flag if that's
	// available, if not assume current dir.
	if *staticPath != "" {
		http.Handle("/x/", http.StripPrefix("/x/", http.FileServer(http.Dir(*staticPath))))
	} else {
		path, err := os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
		http.Handle("/x/", http.StripPrefix("/x/", http.FileServer(http.Dir(path+"/static"))))
	}

	// Streaming channels
	// Handle websocket clients. WS must come up first, so reconnecting clients won't fall back to LP
	http.HandleFunc("/v0/channels", serveWebSocket)
	// Handle long polling clients
	http.HandleFunc("/v0/channels/lp", serveLongPoll)

	log.Printf("Listening on [%s]", config.Listen)
	if err := listenAndServe(config.Listen, signalHandler()); err != nil {
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
