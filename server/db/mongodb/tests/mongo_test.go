// Adapter test.
// Create GetAdapter function inside your db backend adapter package (like one inside mongodb adapter) and
// uncomment your db backend package ('backend' named package) and run tests

package tests

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"testing"

	jcr "github.com/DisposaBoy/JsonConfigReader"
	adapter "github.com/tinode/chat/server/db"

	b "go.mongodb.org/mongo-driver/bson"
	mdb "go.mongodb.org/mongo-driver/mongo"
	mdbopts "go.mongodb.org/mongo-driver/mongo/options"

	//backend "github.com/tinode/chat/server/db/rethinkdb"
	//backend "github.com/tinode/chat/server/db/mysql"
	backend "github.com/tinode/chat/server/db/mongodb"
	"github.com/tinode/chat/server/store/types"
)

type configFile struct {
	TestingConfig json.RawMessage `json:"testing_config"`
}
type configType struct {
	// 16-byte key for XTEA. Used to initialize types.UidGenerator.
	UidKey []byte `json:"uid_key"`
	// If Reset=true test will recreate database every time it runs
	Reset bool `json:"reset_db_data"`
	// Configurations for individual adapters.
	Adapters map[string]json.RawMessage `json:"adapters"`
}

var uGen types.UidGenerator
var config configType
var adp adapter.Adapter
var db *mdb.Database
var ctx context.Context

func TestOne(t *testing.T) {
	//var err error
	//if _, err = adp.UserGet(types.ParseUserId("usr07ZtlTZfaXo")); err != nil {
	//	t.Error("TestUserCreate failed: ", err)
	//}

	//for i := 0; i < 10; i++ {
	//	log.Println(uGen.GetStr())
	//}

	//_ = adp.UserDelete(types.ParseUserId("usrdiuMKIEz1b4"), true)
}

func TestCreateDb(t *testing.T) {
	if err := adp.CreateDb(config.Reset); err != nil {
		t.Fatal(err)
	}
}

func TestUserCreate(t *testing.T) {
	for _, user := range users {
		if err := adp.UserCreate(&user); err != nil {
			t.Error(err)
		}
	}
	count, err := db.Collection("users").CountDocuments(ctx, b.M{})
	if err != nil {
		t.Error(err)
	}
	if count == 0 {
		t.Error("No users created!")
	}
}

func TestUserGet(t *testing.T) {
	// Test not found
	user, err := adp.UserGet(types.ParseUserId("dummyuserid"))
	if err == nil && user != nil {
		t.Error("user should be nil.")
	}

	user, err = adp.UserGet(types.ParseUserId("usr" + users[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*user, users[0]) {
		t.Errorf(mismatchErrorString("User", *user, users[0]))
	}
}

func TestUserGetAll(t *testing.T) {
	// Test not found
	resultUsers, err := adp.UserGetAll(types.ParseUserId("dummyuserid"), types.ParseUserId("otherdummyid"))
	if err == nil && resultUsers != nil {
		t.Error("resultUsers should be nil.")
	}

	resultUsers, err = adp.UserGetAll(types.ParseUserId("usr"+users[0].Id), types.ParseUserId("usr"+users[1].Id))
	if err != nil {
		t.Fatal(err)
	}
	if len(resultUsers) != 2 {
		t.Fatal(mismatchErrorString("resultUsers length", len(resultUsers), 2))
	}
	for i, usr := range resultUsers {
		if !reflect.DeepEqual(usr, users[i]) {
			t.Error(mismatchErrorString("User", usr, users[i]))
		}
	}
}

func mismatchErrorString(key string, got, want interface{}) string {
	return fmt.Sprintf("%v mismatch:\nGot  = %v\nWant = %v", key, got, want)
}

func initConnectionToDb() {
	var adpConfig struct {
		Addresses interface{} `json:"addresses,omitempty"`
		Database  string      `json:"database,omitempty"`
	}

	if err := json.Unmarshal(config.Adapters[adp.GetName()], &adpConfig); err != nil {
		log.Fatal("adapter mongodb failed to parse config: " + err.Error())
	}

	var opts mdbopts.ClientOptions

	if adpConfig.Addresses == nil {
		opts.SetHosts([]string{"localhost:27017"})
	} else if host, ok := adpConfig.Addresses.(string); ok {
		opts.SetHosts([]string{host})
	} else if hosts, ok := adpConfig.Addresses.([]string); ok {
		opts.SetHosts(hosts)
	} else {
		log.Fatal("adapter mongodb failed to parse config.Addresses")
	}

	if adpConfig.Database == "" {
		adpConfig.Database = "tinode_test"
	}

	ctx = context.Background()
	conn, err := mdb.Connect(ctx, &opts)
	if err != nil {
		log.Fatal(err)
	}

	db = conn.Database(adpConfig.Database)
}

func init() {
	adp = backend.GetAdapter()
	conffile := flag.String("config", "./test.conf", "config of the database connection")

	var configFile configFile
	if file, err := os.Open(*conffile); err != nil {
		log.Fatal("Failed to read config file:", err)
	} else if err = json.NewDecoder(jcr.New(file)).Decode(&configFile); err != nil {
		log.Fatal("Failed to parse config file:", err)
	}
	if err := json.Unmarshal(configFile.TestingConfig, &config); err != nil {
		log.Fatal("Failed to parse config: " + err.Error() + "(" + string(configFile.TestingConfig) + ")")
	}

	if adp == nil {
		log.Fatal("Database adapter is missing")
	}
	if adp.IsOpen() {
		log.Print("Connection is already opened")
	}
	if err := uGen.Init(77, config.UidKey); err != nil {
		log.Fatal("Failed to init snowflake: " + err.Error())
	}

	err := adp.Open(config.Adapters[adp.GetName()])
	if err != nil {
		log.Fatal(err)
	}

	initConnectionToDb()
	initData()
}
