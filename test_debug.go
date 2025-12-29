//go:build mysql
// +build mysql

package main

import (
"database/sql"
"encoding/json"
"flag"
"fmt"
"log"
"os"

_ "github.com/go-sql-driver/mysql"
"github.com/jmoiron/sqlx"
"github.com/tinode/chat/server/db/common/test_data"
backend "github.com/tinode/chat/server/db/mysql"
"github.com/tinode/chat/server/logs"
"github.com/tinode/chat/server/store/types"
jcr "github.com/tinode/jsonco"
)

func main() {
	logs.Init(os.Stderr, "stdFlags")
	adp := backend.GetTestAdapter()
	conffile := flag.String("config", "./server/db/mysql/tests/test.conf", "config of the database connection")
	flag.Parse()

	var config struct {
		Reset    bool
		Adapters map[string]json.RawMessage
	}
	if file, err := os.Open(*conffile); err != nil {
		log.Fatal("Failed to read config file:", err)
	} else if err = json.NewDecoder(jcr.New(file)).Decode(&config); err != nil {
		log.Fatal("Failed to parse config file:", err)
	}

	err := adp.Open(config.Adapters[adp.GetName()])
	if err != nil {
		log.Fatal(err)
	}
	defer adp.Close()

	db := adp.GetTestDB().(*sqlx.DB)
	testData := test_data.InitTestData()

	// Create DB
	if err := adp.CreateDb(true); err != nil {
		log.Fatal(err)
	}
	db = adp.GetTestDB().(*sqlx.DB)

	// Create users and topics
	for _, user := range testData.Users {
		if err := adp.UserCreate(user); err != nil {
			log.Fatal(err)
		}
	}

	for i, tpc := range testData.Topics {
		if i == 0 || i > 2 {
			if err := adp.TopicCreate(tpc); err != nil {
				log.Fatal(err)
			}
		}
	}

	// Share topics
	if err := adp.TopicShare(testData.Topics[0].Id, testData.Subs); err != nil {
		log.Fatal(err)
	}

	// Save messages
	for _, msg := range testData.Msgs {
		if err := adp.MessageSave(msg); err != nil {
			log.Fatal(err)
		}
	}

	// Soft delete
	for _, msg := range testData.Msgs {
		if len(msg.DeletedFor) > 0 {
			for _, del := range msg.DeletedFor {
				toDel := types.DelMessage{
					Topic:       msg.Topic,
					DeletedFor:  del.User,
					DelId:       del.DelId,
					SeqIdRanges: []types.Range{{Low: msg.SeqId}},
				}
				if err := adp.MessageDeleteList(msg.Topic, &toDel); err != nil {
					log.Fatal(err)
				}
			}
		}
	}

	// Check database
	fmt.Println("\n=== MESSAGES ===")
	var rows *sql.Rows
	rows, err = db.Query("SELECT seqid, topic, delid FROM messages WHERE topic=?", testData.Topics[0].Id)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var seqid, delid int
		var topic string
		if err := rows.Scan(&seqid, &topic, &delid); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("seqid=%d, topic=%s, delid=%d\n", seqid, topic, delid)
	}
	rows.Close()

	fmt.Println("\n=== DELLOG ===")
	rows, err = db.Query("SELECT topic, deletedfor, delid, low, hi FROM dellog WHERE topic=?", testData.Topics[0].Id)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var topic string
		var deletedfor int64
		var delid, low, hi int
		if err := rows.Scan(&topic, &deletedfor, &delid, &low, &hi); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("topic=%s, deletedfor=%d, delid=%d, low=%d, hi=%d\n", topic, deletedfor, delid, low, hi)
	}
	rows.Close()

	fmt.Println("\n=== TEST DATA ===")
	for i, msg := range testData.Msgs[:3] {
		fmt.Printf("msg[%d]: seqid=%d, topic=%s, deletedFor=%+v\n", i, msg.SeqId, msg.Topic, msg.DeletedFor)
	}
}
