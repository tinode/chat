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
	_ "github.com/tinode/chat/server/db/rethinkdb"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"
)

const (
	IDLETIMEOUT  = time.Second * 55 // Terminate session after this timeout.
	TOPICTIMEOUT = time.Minute * 5  // Tear down topic after this period of silence.

	// API version
	VERSION = "0.4"

	// Lofetime of authentication tokens
	TOKEN_LIFETIME_DEFAULT = time.Hour * 12     // 12 hours
	TOKEN_LIFETIME_MAX     = time.Hour * 24 * 7 // 1 week

	DEFAULT_AUTH_ACCESS = types.ModePublic
	DEFAULT_ANON_ACCESS = types.ModeNone
)

// Build timestamp set by the compiler
var buildstamp = ""

var globals struct {
	hub *Hub

	sessionStore *SessionStore
}

type configType struct {
	Listen        string          `json:"listen"`
	Adapter       string          `json:"db_adapter"`
	AdapterConfig json.RawMessage `json:"adapter_config"`
}

func main() {
	// For serving static content
	path, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Home dir: '%s'", path)

	log.Printf("Server started with processes: %d", runtime.GOMAXPROCS(runtime.NumCPU()))

	var configfile = flag.String("config", "./config", "Path to config file")
	flag.Parse()

	log.Printf("Using config from: '%s'", *configfile)

	var config configType
	if raw, err := ioutil.ReadFile(*configfile); err != nil {
		log.Fatal(err)
	} else if err = json.Unmarshal(raw, &config); err != nil {
		log.Fatal(err)
	}

	err = store.Open(config.Adapter, string(config.AdapterConfig))
	if err != nil {
		log.Fatal("failed to connect to DB: ", err)
	}
	defer store.Close()

	globals.sessionStore = NewSessionStore(2 * time.Hour)
	globals.hub = newHub()

	// Static content from http://<host>/x/: just read files from disk
	http.Handle("/x/", http.StripPrefix("/x/", http.FileServer(http.Dir(path+"/static"))))

	// Streaming channels
	// Handle websocket clients. WS must come up first, so reconnecting clients won't fall back to LP
	http.HandleFunc("/v0/channels", serveWebSocket)
	// Handle long polling clients
	http.HandleFunc("/v0/channels/lp", serveLongPoll)

	log.Printf("Listening on [%s]", config.Listen)
	log.Fatal(http.ListenAndServe(config.Listen, nil))
}

func getApiKey(req *http.Request) string {
	apikey := req.FormValue("apikey")
	if apikey == "" {
		apikey = req.Header.Get("X-Tinode-APIKey")
	}
	return apikey
}
