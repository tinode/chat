package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/tinode/chat/server/db/mongodb"
	_ "github.com/tinode/chat/server/db/mysql"
	_ "github.com/tinode/chat/server/db/rethinkdb"
	"github.com/tinode/chat/server/store"
	jcr "github.com/tinode/jsonco"
)

type configType struct {
	StoreConfig json.RawMessage `json:"store_config"`
}

type vCardy struct {
	Fn    string `json:"fn"`
	Photo string `json:"photo"`
	Type  string `json:"type"`
}

type tPrivate struct {
	Comment string `json:"comment"`
}

// DefAccess is default access mode.
type DefAccess struct {
	Auth string `json:"auth"`
	Anon string `json:"anon"`
}

/*
User object in data.json
   "createdAt": "-140h",
   "email": "alice@example.com",
   "tel": "17025550001",
   "passhash": "alice123",
   "private": {"comment": "some comment 123"},
   "public": {"fn": "Alice Johnson", "photo": "alice-64.jpg", "type": "jpg"},
   "state": "ok",
   "authLevel": "auth",
   "status": {
     "text": "DND"
   },
   "username": "alice",
	"tags": ["tag1"],
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
	AuthLevel   string      `json:"authLevel"`
	Username    string      `json:"username"`
	Password    string      `json:"passhash"`
	Private     tPrivate    `json:"private"`
	Public      vCardy      `json:"public"`
	State       string      `json:"state"`
	Status      interface{} `json:"status"`
	AddressBook []string    `json:"addressBook"`
	Tags        []string    `json:"tags"`
}

/*
GroupTopic object in data.json

   "createdAt": "-128h",
   "name": "*ABC",
   "owner": "carol",
   "channel": true,
   "public": {"fn": "Let's talk about flowers", "photo": "abc-64.jpg", "type": "jpg"}
*/
type GroupTopic struct {
	CreatedAt    string    `json:"createdAt"`
	Name         string    `json:"name"`
	Owner        string    `json:"owner"`
	Channel      bool      `json:"channel"`
	Public       vCardy    `json:"public"`
	Access       DefAccess `json:"access"`
	Tags         []string  `json:"tags"`
	OwnerPrivate tPrivate  `json:"ownerPrivate"`
}

/*
GroupSub object in data.json

 "createdAt": "-112h",
 "private": "My super cool group topic",
 "topic": "*ABC",
 "user": "alice",
 "asChan: false,
 "want": "JRWPSA",
 "have": "JRWP"
*/
type GroupSub struct {
	CreatedAt string   `json:"createdAt"`
	Private   tPrivate `json:"private"`
	Topic     string   `json:"topic"`
	User      string   `json:"user"`
	AsChan    bool     `json:"asChan"`
	Want      string   `json:"want"`
	Have      string   `json:"have"`
}

/*
P2PUser topic in data.json

"createdAt": "-117h",
"users": [
  {"name": "eve", "private": {"comment":"ho ho"}, "want": "JRWP", "have": "N"},
  {"name": "alice", "private": {"comment": "ha ha"}}
]
*/
type P2PUser struct {
	Name    string   `json:"name"`
	Private tPrivate `json:"private"`
	Want    string   `json:"want"`
	Have    string   `json:"have"`
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
	Users       []User                   `json:"users"`
	Grouptopics []GroupTopic             `json:"grouptopics"`
	Groupsubs   []GroupSub               `json:"groupsubs"`
	P2psubs     []P2PSub                 `json:"p2psubs"`
	Messages    []string                 `json:"messages"`
	Forms       []map[string]interface{} `json:"forms"`
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
	var reset = flag.Bool("reset", false, "force database reset")
	var upgrade = flag.Bool("upgrade", false, "perform database version upgrade")
	var noInit = flag.Bool("no_init", false, "check that database exists but don't create if missing")
	var datafile = flag.String("data", "", "name of file with sample data to load")
	var conffile = flag.String("config", "./tinode.conf", "config of the database connection")

	flag.Parse()

	var data Data
	if *datafile != "" && *datafile != "-" {
		raw, err := ioutil.ReadFile(*datafile)
		if err != nil {
			log.Fatalln("Failed to read sample data file:", err)
		}
		err = json.Unmarshal(raw, &data)
		if err != nil {
			log.Fatalln("Failed to parse sample data:", err)
		}
	}

	rand.Seed(time.Now().UnixNano())
	data.datapath, _ = filepath.Split(*datafile)

	var config configType
	if file, err := os.Open(*conffile); err != nil {
		log.Fatalln("Failed to read config file:", err)
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
	}

	err := store.Open(1, config.StoreConfig)
	defer store.Close()

	log.Println("Database", store.GetAdapterName(), store.GetAdapterVersion())

	if err != nil {
		if strings.Contains(err.Error(), "Database not initialized") {
			if *noInit {
				log.Fatalln("Database not found.")
			}
			log.Println("Database not found. Creating.")
		} else if strings.Contains(err.Error(), "Invalid database version") {
			msg := "Wrong DB version: expected " + strconv.Itoa(store.GetAdapterVersion()) + ", got " +
				strconv.Itoa(store.GetDbVersion()) + "."
			if *reset {
				log.Println(msg, "Dropping and recreating the database.")
			} else if *upgrade {
				log.Println(msg, "Upgrading the database.")
			} else {
				log.Fatalln(msg, "Use --reset to reset, --upgrade to upgrade.")
			}
		} else {
			log.Fatalln("Failed to init DB adapter:", err)
		}
	} else if *reset {
		log.Println("Database reset requested")
	} else {
		log.Println("Database exists, DB version is correct. All done.")
		os.Exit(0)
	}

	if *upgrade {
		// Upgrade DB from one version to another.
		err = store.UpgradeDb(config.StoreConfig)
		if err == nil {
			log.Println("Database successfully upgraded.")
		}
	} else {
		// Reset or create DB
		err = store.InitDb(config.StoreConfig, true)
		if err == nil {
			var action string
			if *reset {
				action = "reset"
			} else {
				action = "initialized"
			}
			log.Println("Database", action)
		}
	}

	if err != nil {
		log.Fatalln("Failed to init DB:", err)
	}

	if !*upgrade {
		genDb(&data)
	} else if len(data.Users) > 0 {
		log.Println("Sample data ignored. All done.")
	}
	os.Exit(0)
}
