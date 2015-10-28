package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
)

type Data struct {
	Users         []map[string]interface{} `json:"users"`
	Grouptopics   []map[string]interface{} `json:"grouptopics"`
	Subscriptions []map[string]interface{} `json:"subscriptions"`
	Messages      []string                 `json:"messages"`
}

// Name generator for group topics
func _getRandomString() string {
	buf := make([]byte, 9)
	_, err := rand.Read(buf)
	if err != nil {
		panic("getRandomString: failed to generate a random string: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(buf)
}

func genTopicName() string {
	return "grp" + _getRandomString()
}

func main() {

	var reset = flag.Bool("reset", false, "first delete the database if one exists")
	var datafile = flag.String("data", "", "path to sample data to load")
	var conffile = flag.String("config", "./config", "config of the database connection")
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
		conf, err := ioutil.ReadFile(*datafile)
		if err != nil {
			log.Fatal(err)
		}

		gen_rethink(*reset, string(conf), &data)
	} else {
		log.Println("No config provided. Exiting.")
	}
}
