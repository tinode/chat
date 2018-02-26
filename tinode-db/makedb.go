package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"math/rand"
	"path/filepath"
	"time"

	_ "github.com/tinode/chat/server/db/mysql"
	_ "github.com/tinode/chat/server/db/rethinkdb"
	"github.com/tinode/chat/server/store"
)

type configType struct {
	StoreConfig json.RawMessage `json:"store_config"`
}

type vCardy struct {
	Fn    string `json:"fn"`
	Photo string `json:"photo"`
	Type  string `json:"type"`
}

/*
User object in data.json
   "createdAt": "-140h",
   "email": "alice@example.com",
   "tel": "17025550001",
   "passhash": "alice123",
   "private": "some comment 123",
   "public": {"fn": "Alice Johnson", "photo": "alice-64.jpg", "type": "jpg"},
   "state": 1,
   "status": {
    "text": "DND"
   },
   "username": "alice",
	"addressBook": ["email:bob@example.com", "email:carol@example.com", "email:dave@example.com",
		"email:eve@example.com","email:frank@example.com","email:george@example.com","email:tob@example.com",
		"tel:17025550001", "tel:17025550002", "tel:17025550003", "tel:17025550004", "tel:17025550005",
		"tel:17025550006", "tel:17025550007", "tel:17025550008", "tel:17025550009"]
  }
*/
type User struct {
	CreatedAt   string      `json:"createdAt"`
	Email       string      `json:"email"`
	Tel         string      `json:"tel"`
	Username    string      `json:"username"`
	Password    string      `json:"passhash"`
	Private     interface{} `json:"private"`
	Public      vCardy      `json:"public"`
	State       int         `json:"state"`
	Status      interface{} `json:"status"`
	AddressBook []string    `json:"addressBook"`
}

/*
GroupTopic object in data.json

   "createdAt": "-128h",
   "name": "*ABC",
   "owner": "carol",
   "public": {"fn": "Let's talk about flowers", "photo": "abc-64.jpg", "type": "jpg"}
*/
type GroupTopic struct {
	CreatedAt    string      `json:"createdAt"`
	Name         string      `json:"name"`
	Owner        string      `json:"owner"`
	Public       vCardy      `json:"public"`
	OwnerPrivate interface{} `json:"ownerPrivate"`
}

/*
GroupSub object in data.json

 "createdAt": "-112h",
 "private": "My super cool group topic",
 "topic": "*ABC",
 "user": "alice"
 "want": "JRWPSA",
 "have": "JRWP"
*/
type GroupSub struct {
	CreatedAt string      `json:"createdAt"`
	Private   interface{} `json:"private"`
	Topic     string      `json:"topic"`
	User      string      `json:"user"`
	Want      string      `json:"want"`
	Have      string      `json:"have"`
}

/*
P2PUser topic in data.json

"createdAt": "-117h",
"users": [
  {"name": "eve", "private": "ho ho", "want": "JRWP", "have": "N"},
  {"name": "alice", "private": "ha ha"}
]
*/
type P2PUser struct {
	Name    string      `json:"name"`
	Private interface{} `json:"private"`
	Want    string      `json:"want"`
	Have    string      `json:"have"`
}

// P2PSub is a p2p subscription in data.json
type P2PSub struct {
	CreatedAt string    `json:"createdAt"`
	Users     []P2PUser `json:"users"`
	// Cached value 'user1:user2' as a surrogare topic name
	pair string
}

// Data is a message in data.json.
type Data struct {
	Users       []User       `json:"users"`
	Grouptopics []GroupTopic `json:"grouptopics"`
	Groupsubs   []GroupSub   `json:"groupsubs"`
	P2psubs     []P2PSub     `json:"p2psubs"`
	Messages    []string     `json:"messages"`
	datapath    string
}

// Generate random string as a name of the group topic
func genTopicName() string {
	return "grp" + store.GetUidString()
}

// Generates password of length n
func getPassword(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-/.+?=&"

	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func main() {
	var reset = flag.Bool("reset", false, "first delete the database if one exists")
	var datafile = flag.String("data", "", "name of file with sample data")
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
	data.datapath, _ = filepath.Split(*datafile)
	if *conffile != "" {
		rand.Seed(time.Now().UnixNano())

		var config configType
		if raw, err := ioutil.ReadFile(*conffile); err != nil {
			log.Fatal(err)
		} else if err = json.Unmarshal(raw, &config); err != nil {
			log.Fatal(err)
		}

		genDb(*reset, string(config.StoreConfig), &data)
	} else {
		log.Println("No config provided. Exiting.")
	}
}
