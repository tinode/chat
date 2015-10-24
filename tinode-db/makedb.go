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
	var filename = flag.String("data", "(>*<)", "path to sample data to load")
	var dbsource = flag.String("db",
		"rethinkdb://localhost:28015/tinode?authKey=&discover=false&maxIdle=&maxOpen=&timeout=&workerId=1&uidkey=la6YsO-bNX_-XIkOqc5Svw==",
		"data source name and configuration")
	flag.Parse()

	if *filename == "" {
		*filename = "./data.json"
	}

	var data Data

	if *filename != "(>*<)" {
		raw, err := ioutil.ReadFile(*filename)
		if err != nil {
			log.Fatal(err)
		}
		err = json.Unmarshal(raw, &data)
		if err != nil {
			log.Fatal(err)
		}
	}

	gen_rethink(*reset, *dbsource, &data)
}
