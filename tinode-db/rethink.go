package main

import (
	"io/ioutil"
	"log"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/tinode/chat/server/auth_basic"
	_ "github.com/tinode/chat/server/db/rethinkdb"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

func gen_rethink(reset bool, dbsource string, data *Data) {
	var err error

	log.Println("Opening DB...")

	err = store.Open(dbsource)
	if err != nil {
		log.Fatal("Failed to connect to DB: ", err)
	}
	defer store.Close()

	log.Println("Initializing DB...")

	err = store.InitDb(reset)
	if err != nil {
		if strings.Contains(err.Error(), " already exists") {
			log.Println("DB already exists, NOT reinitializing")
		} else {
			log.Fatal("Failed to init DB: ", err)
		}
	} else {
		log.Println("DB successfully initialized")

	}
	if data.Users == nil {
		log.Println("No data provided, stopping")
		return
	}

	nameIndex := make(map[string]string, len(data.Users))

	log.Println("Generating users...")

	for _, uu := range data.Users {

		user := types.User{
			State: int(uu["state"].(float64)),
			Access: types.DefaultAccess{
				Auth: types.ModeCP2P,
				Anon: types.ModeNone,
			},
			Public: parsePublic(uu["public"], data.datapath),
		}
		user.CreatedAt = getCreatedTime(uu["createdAt"])

		if uu["email"] != nil || uu["tel"] != nil {
			user.Tags = make([]string, 0)
			if uu["email"] != nil {
				user.Tags = append(user.Tags, "email:"+uu["email"].(string))
			}
			if uu["tel"] != nil {
				user.Tags = append(user.Tags, "tel:"+uu["tel"].(string))
			}
		}

		// store.Users.Create will subscribe user to !me topic but won't create a !me topic
		if _, err := store.Users.Create(&user, uu["private"]); err != nil {
			log.Fatal(err)
		}

		// Add authentication record
		auth_handler := store.GetAuthHandler("basic")
		if _, authErr := auth_handler.AddRecord(user.Uid(),
			[]byte(uu["username"].(string)+":"+uu["passhash"].(string)), 0); authErr.IsError() {

			log.Fatal(authErr.Err)
		}
		nameIndex[uu["username"].(string)] = user.Id

		// Add address book as fnd.private
		if uu["addressBook"] != nil {
			if err := store.Subs.Update(user.Uid().FndName(), user.Uid(),
				map[string]interface{}{"Private": uu["addressBook"]}); err != nil {

				log.Fatal(err)
			}
		}
		log.Printf("Created user '%s' as %s (%d)", uu["username"].(string), user.Id, user.Uid())
	}

	log.Println("Generating group topics...")

	for _, gt := range data.Grouptopics {
		name := genTopicName()
		nameIndex[gt["name"].(string)] = name

		topic := &types.Topic{
			ObjHeader: types.ObjHeader{Id: name},
			Access: types.DefaultAccess{
				Auth: types.ModeCPublic,
				Anon: types.ModeCReadOnly,
			},
			Public: parsePublic(gt["public"], data.datapath)}
		var owner types.Uid
		if gt["owner"] != nil {
			owner = types.ParseUid(nameIndex[gt["owner"].(string)])
			topic.GiveAccess(owner, types.ModeCFull, types.ModeCFull)
		}
		topic.CreatedAt = getCreatedTime(gt["createdAt"])

		if err = store.Topics.Create(topic, owner, gt["private"]); err != nil {
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

		// Assign default access mode
		s0want := types.ModeCP2P
		s0given := types.ModeCP2P
		s1want := types.ModeCP2P
		s1given := types.ModeCP2P

		// Check of non-default access mode was provided
		if subs[0]["modeWant"] != nil {
			if err := s0want.UnmarshalText([]byte(subs[0]["modeWant"].(string))); err != nil {
				log.Fatal(err)
			}
		}
		if subs[0]["modeHave"] != nil {
			if err := s0given.UnmarshalText([]byte(subs[0]["modeHave"].(string))); err != nil {
				log.Fatal(err)
			}
		}
		if subs[1]["modeWant"] != nil {
			if err := s1want.UnmarshalText([]byte(subs[1]["modeWant"].(string))); err != nil {
				log.Fatal(err)
			}
		}
		if subs[1]["modeHave"] != nil {
			if err := s1given.UnmarshalText([]byte(subs[1]["modeHave"].(string))); err != nil {
				log.Fatal(err)
			}
		}

		log.Printf("Processing %s (%s), %s, %s", pair, topic, uid1.String(), uid2.String())
		err := store.Topics.CreateP2P(
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

		want := types.ModeCPublic
		given := types.ModeCPublic
		if ss["modeWant"] != nil {
			if err := want.UnmarshalText([]byte(ss["modeWant"].(string))); err != nil {
				log.Fatal(err)
			}
		}
		if ss["modeHave"] != nil {
			if err := given.UnmarshalText([]byte(ss["modeHave"].(string))); err != nil {
				log.Fatal(err)
			}
		}
		// Define topic name
		name := u2
		if !types.ParseUid(u2).IsZero() {
			// skip p2p subscriptions
			continue
		}

		log.Printf("Sharing '%s' with '%s'", ss["topic"].(string), ss["user"].(string))

		if err = store.Subs.Create(&types.Subscription{
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
	seqIds := map[string]int{}
	var oldFrom types.Uid
	var oldTopic string
	toInsert := 80
	// Starting 4 days ago
	timestamp := time.Now().UTC().Round(time.Millisecond).Add(time.Second * time.Duration(-3600*24*4))
	subIdx := rand.Intn(len(data.Subscriptions))
	for i := 0; i < toInsert; i++ {
		// At least 20% of subsequent messages should come from the same user in the same topic.
		if rand.Intn(5) > 0 {
			subIdx = rand.Intn(len(data.Subscriptions))
		}

		sub := data.Subscriptions[subIdx]
		topic := nameIndex[sub["topic"].(string)]
		from := types.ParseUid(nameIndex[sub["user"].(string)])
		if topic == oldTopic && from == oldFrom {
			toInsert++
			continue
		}
		oldTopic, oldFrom = topic, from

		if uid := types.ParseUid(topic); !uid.IsZero() {
			topic = uid.P2PName(from)
		}

		seqIds[topic]++
		seqId := seqIds[topic]
		str := data.Messages[rand.Intn(len(data.Messages))]
		// Max time between messages is 2 hours, averate - 1 hour, time is increasing as seqId increases
		timestamp = timestamp.Add(time.Millisecond * time.Duration(rand.Intn(3600*2*1000)))
		msg := types.Message{
			ObjHeader: types.ObjHeader{CreatedAt: timestamp},
			SeqId:     seqId,
			Topic:     topic,
			From:      from.String(),
			Content:   str}
		if err = store.Messages.Save(&msg); err != nil {
			log.Fatal(err)
		}
		log.Printf("Message %d at %v to '%s' '%s'", msg.SeqId, msg.CreatedAt, topic, str)
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

type PhotoStruct struct {
	Type string `gorethink:"type"`
	Data []byte `gorethink:"data"`
}

type Vcard struct {
	Fn    string       `gorethink:"fn"`
	Photo *PhotoStruct `gorethink:"photo,omitempty"`
}

// {"fn": "Alice Johnson", "photo": "alice-128.jpg"}
func parsePublic(public interface{}, path string) interface{} {
	var photo *PhotoStruct
	var err error

	if public == nil {
		return nil
	}

	vcard := public.(map[string]interface{})

	if fname, ok := vcard["photo"]; ok {
		if fname != nil {
			photo = &PhotoStruct{Type: vcard["type"].(string)}
			dir, _ := filepath.Split(fname.(string))
			if dir == "" {
				dir = path
			}
			photo.Data, err = ioutil.ReadFile(filepath.Join(dir, fname.(string)))
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	return Vcard{Fn: vcard["fn"].(string), Photo: photo}
}
