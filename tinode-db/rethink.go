package main

import (
	_ "github.com/tinode/chat/server/db/rethinkdb"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
	"log"
	"math/rand"
	"time"
)

func gen_rethink(data *Data) {
	var err error

	log.Println("Opening DB...")

	err = store.Open("rethinkdb://localhost:28015/tinode?authKey=&discover=false&maxIdle=&maxOpen=&timeout=&workerId=1&uidkey=la6YsO-bNX_-XIkOqc5Svw==")
	if err != nil {
		log.Fatal("failed to connect to DB: ", err)
	}
	defer store.Close()

	log.Println("Resetting DB...")

	store.ResetDb()

	if data.Users == nil {
		log.Println("No data provided, stopping")
		return
	}

	nameIndex := make(map[string]string, len(data.Users))

	log.Println("Generating users...")

	for _, uu := range data.Users {
		if uu["createdAt"] != nil {

		}
		user := types.User{
			State:    int(uu["state"].(float64)),
			Username: uu["username"].(string),
			Access: types.DefaultAccess{
				Auth: types.ModePublic,
				Anon: types.ModeNone,
			},
			Public: uu["public"],
		}
		user.CreatedAt = getCreatedTime(uu["createdAt"])

		// store.Users.Create will subscribe user to !me topic but won't create a !me topic
		if _, err := store.Users.Create(0, &user, uu["passhash"].(string), uu["private"]); err != nil {
			log.Fatal(err)
		}

		nameIndex[user.Username] = user.Id

		log.Printf("Created user '%s' as %s (%d)", user.Username, user.Id, user.Uid())
	}

	log.Println("Generating group topics...")

	for _, gt := range data.Grouptopics {
		name := genTopicName()
		nameIndex[gt["name"].(string)] = name

		topic := &types.Topic{Name: name, Public: gt["public"]}
		var owner types.Uid
		if gt["owner"] != nil {
			owner = types.ParseUid(nameIndex[gt["owner"].(string)])
			topic.GiveAccess(owner, types.ModeFull, types.ModeFull)
		}
		topic.CreatedAt = getCreatedTime(gt["createdAt"])

		if err = store.Topics.Create(0, topic, owner, gt["private"]); err != nil {
			log.Fatal(err)
		}
		log.Printf("Created topic '%s' as %s", gt["name"].(string), name)
	}

	log.Println("Generating P2P subscriptions...")

	p2pIndex := map[string][]map[string]interface{}{}

	for _, ss := range data.Subscriptions {
		u1 := ss["user"].(string)
		u2 := ss["topic"].(string)

		if u2[0] == '*' {
			// skip group topics
			continue
		}

		var pair string
		var idx int
		if u1 < u2 {
			pair = u1 + ":" + u2
			idx = 0
		} else {
			pair = u2 + ":" + u1
			idx = 1
		}
		if _, ok := p2pIndex[pair]; !ok {
			p2pIndex[pair] = make([]map[string]interface{}, 2)
		}

		p2pIndex[pair][idx] = ss
	}

	log.Printf("Collected p2p pairs: %d\n", len(p2pIndex))

	for pair, subs := range p2pIndex {
		uid1 := types.ParseUid(nameIndex[subs[0]["user"].(string)])
		uid2 := types.ParseUid(nameIndex[subs[1]["user"].(string)])
		topic := uid1.P2PName(uid2)
		created0 := getCreatedTime(subs[0]["createdAt"])
		created1 := getCreatedTime(subs[1]["createdAt"])
		var s0want, s0given, s1want, s1given types.AccessMode
		if err := s0want.UnmarshalText([]byte(subs[0]["modeWant"].(string))); err != nil {
			log.Fatal(err)
		}
		if err := s0given.UnmarshalText([]byte(subs[0]["modeHave"].(string))); err != nil {
			log.Fatal(err)
		}

		if err := s1want.UnmarshalText([]byte(subs[1]["modeWant"].(string))); err != nil {
			log.Fatal(err)
		}
		if err := s1given.UnmarshalText([]byte(subs[1]["modeHave"].(string))); err != nil {
			log.Fatal(err)
		}

		log.Printf("Processing %s (%s), %s, %s", pair, topic, uid1.String(), uid2.String())
		err := store.Topics.CreateP2P(0,
			&types.Subscription{
				ObjHeader: types.ObjHeader{CreatedAt: created0},
				User:      uid1.String(),
				Topic:     topic,
				ModeWant:  s0want,
				ModeGiven: s0given,
				Private:   subs[0]["private"]},
			&types.Subscription{
				ObjHeader: types.ObjHeader{CreatedAt: created1},
				User:      uid2.String(),
				Topic:     topic,
				ModeWant:  s1want,
				ModeGiven: s1given,
				Private:   subs[1]["private"]})

		if err != nil {
			log.Fatal(err)
		}
	}

	log.Println("Generating group subscriptions...")

	for _, ss := range data.Subscriptions {

		u1 := nameIndex[ss["user"].(string)]
		u2 := nameIndex[ss["topic"].(string)]

		var want, given types.AccessMode
		if err := want.UnmarshalText([]byte(ss["modeWant"].(string))); err != nil {
			log.Fatal(err)
		}
		if err := given.UnmarshalText([]byte(ss["modeHave"].(string))); err != nil {
			log.Fatal(err)
		}

		// Define topic name
		name := u2
		if !types.ParseUid(u2).IsZero() {
			// skip p2p subscriptions
			continue
		}

		log.Printf("Sharing '%s' with '%s'", ss["topic"].(string), ss["user"].(string))

		if err = store.Subs.Create(0, &types.Subscription{
			ObjHeader: types.ObjHeader{CreatedAt: getCreatedTime(ss["createdAt"])},
			User:      u1,
			Topic:     name,
			ModeWant:  want,
			ModeGiven: given,
			Private:   ss["private"]}); err != nil {

			log.Fatal(err)
		}
	}

	log.Println("Generating messages...")

	rand.Seed(time.Now().UnixNano())
	for i := 0; i < 80; i++ {

		sub := data.Subscriptions[rand.Intn(len(data.Subscriptions))]
		topic := nameIndex[sub["topic"].(string)]
		from := types.ParseUid(nameIndex[sub["user"].(string)])

		if uid := types.ParseUid(topic); !uid.IsZero() {
			topic = uid.P2PName(from)
		}

		str := data.Messages[rand.Intn(len(data.Messages))]
		timestamp := time.Now().UTC().Round(time.Millisecond).Add(time.Second * time.Duration(-1*rand.Intn(3600*24*4))) // spread over the past 4 days
		if err = store.Messages.Save(0, &types.Message{
			ObjHeader: types.ObjHeader{CreatedAt: timestamp},
			Topic:     topic,
			From:      from.String(),
			Content:   str}); err != nil {
			log.Fatal(err)
		}
		log.Printf("Message from '%s' to '%s' (%s) '%s'", from.String(), topic, nameIndex[sub["topic"].(string)], str)
	}
}

func getCreatedTime(v interface{}) time.Time {
	if v != nil {
		dd, err := time.ParseDuration(v.(string))
		if err == nil {
			return time.Now().UTC().Round(time.Millisecond).Add(dd)
		} else {
			log.Fatal(err)
		}
	}
	return time.Time{}
}
