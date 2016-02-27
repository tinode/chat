package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"

	"github.com/tinode/chat/server/store"
)

type configType struct {
	Adapter     string          `json:"use_adapter"`
	StoreConfig json.RawMessage `json:"store_config"`
}

type Data struct {
	Users         []map[string]interface{} `json:"users"`
	Grouptopics   []map[string]interface{} `json:"grouptopics"`
	Subscriptions []map[string]interface{} `json:"subscriptions"`
	Messages      []string                 `json:"messages"`
}

// Generate random string as a name of the group topic
func genTopicName() string {
	return "grp" + store.GetUidString()
}

func main() {

	var reset = flag.Bool("reset", false, "first delete the database if one exists")
	var datafile = flag.String("data", "", "path to sample data to load")
	var conffile = flag.String("config", "./tinode.conf", "config of the database connection")
	flag.Parse()

	var data Data
	if *datafile != "" {
		raw, err := ioutil.ReadFile(*datafile)
		if err != nil {
			log.Fatal(err)
		}
		err = json.Unmarshal(raw, &data)
		if err != nil {
			log.Fatal(err)
		}
	}

	if *conffile != "" {
		var config configType
		if raw, err := ioutil.ReadFile(*conffile); err != nil {
			log.Fatal(err)
		} else if err = json.Unmarshal(raw, &config); err != nil {
			log.Fatal(err)
		}
		if config.Adapter != "rethinkdb" {
			log.Fatal("Unknown adapter '" + config.Adapter + "'")
		}

		gen_rethink(*reset, string(config.StoreConfig), &data)
	} else {
		log.Println("No config provided. Exiting.")
	}
}
