package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"github.com/tinode/chat/server/auth"
	_ "github.com/tinode/chat/server/auth/basic"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

func genDb(data *Data) {
	var err error
	var botAccount string

	if len(data.Users) == 0 {
		log.Println("No data provided, stopping")
		return
	}

	// Add authentication record
	authHandler := store.GetAuthHandler("basic")
	authHandler.Init([]byte(`{"add_to_tags": true}`), "basic")

	nameIndex := make(map[string]string, len(data.Users))

	log.Println("Generating users...")

	for _, uu := range data.Users {
		state, err := types.NewObjState(uu.State)
		if err != nil {
			log.Fatal(err)
		}
		user := types.User{
			State: state,
			Access: types.DefaultAccess{
				Auth: types.ModeCAuth,
				Anon: types.ModeNone,
			},
			Tags:   uu.Tags,
			Public: parsePublic(&uu.Public, data.datapath),
		}
		user.CreatedAt = getCreatedTime(uu.CreatedAt)

		user.Tags = make([]string, 0)
		user.Tags = append(user.Tags, "basic:"+uu.Username)
		if uu.Email != "" {
			user.Tags = append(user.Tags, "email:"+uu.Email)
		}
		if uu.Tel != "" {
			user.Tags = append(user.Tags, "tel:"+uu.Tel)
		}

		// store.Users.Create will subscribe user to !me topic but won't create a !me topic
		if _, err := store.Users.Create(&user, uu.Private); err != nil {
			log.Fatal(err)
		}

		// Save credentials: email and phone number as if they were confirmed.
		if uu.Email != "" {
			if _, err := store.Users.UpsertCred(&types.Credential{
				User:   user.Id,
				Method: "email",
				Value:  uu.Email,
				Done:   true,
			}); err != nil {
				log.Fatal(err)
			}
		}
		if uu.Tel != "" {
			if _, err := store.Users.UpsertCred(&types.Credential{
				User:   user.Id,
				Method: "tel",
				Value:  uu.Tel,
				Done:   true,
			}); err != nil {
				log.Fatal(err)
			}
		}

		authLevel := auth.LevelAuth
		if uu.AuthLevel != "" {
			authLevel = auth.ParseAuthLevel(uu.AuthLevel)
			if authLevel == auth.LevelNone {
				log.Fatal("Unknown authLevel", uu.AuthLevel)
			}
		}
		// Add authentication record
		authHandler := store.GetAuthHandler("basic")
		passwd := uu.Password
		if passwd == "(random)" {
			// Generate random password
			passwd = getPassword(8)
			botAccount = uu.Username
		}
		if _, err := authHandler.AddRecord(&auth.Rec{Uid: user.Uid(), AuthLevel: authLevel},
			[]byte(uu.Username+":"+passwd)); err != nil {

			log.Fatal(err)
		}
		nameIndex[uu.Username] = user.Id

		// Add address book as fnd.private
		if uu.AddressBook != nil && len(uu.AddressBook) > 0 {
			if err := store.Subs.Update(user.Uid().FndName(), user.Uid(),
				map[string]interface{}{"Private": strings.Join(uu.AddressBook, ",")}, true); err != nil {

				log.Fatal(err)
			}
		}

		fmt.Println("usr;" + uu.Username + ";" + user.Uid().UserId() + ";" + passwd)
	}

	if botAccount == "" && len(data.Users) > 0 {
		botAccount = data.Users[0].Username
	}

	log.Println("Generating group topics...")

	for _, gt := range data.Grouptopics {
		name := genTopicName()
		nameIndex[gt.Name] = name

		accessAuth := types.ModeCPublic
		if gt.Access.Auth != "" {
			if err := accessAuth.UnmarshalText([]byte(gt.Access.Auth)); err != nil {
				log.Fatal("Invalid Auth access mode", gt.Access.Auth, err)
			}
		}
		accessAnon := types.ModeCReadOnly
		if gt.Access.Anon != "" {
			if err := accessAnon.UnmarshalText([]byte(gt.Access.Anon)); err != nil {
				log.Fatal("Invalid Anon access mode", gt.Access.Anon, err)
			}
		}
		topic := &types.Topic{
			ObjHeader: types.ObjHeader{Id: name},
			Access: types.DefaultAccess{
				Auth: accessAuth,
				Anon: accessAnon,
			},
			Tags:   gt.Tags,
			Public: parsePublic(&gt.Public, data.datapath)}
		var owner types.Uid
		if gt.Owner != "" {
			owner = types.ParseUid(nameIndex[gt.Owner])
			if owner.IsZero() {
				log.Fatal("Invalid owner", gt.Owner, "for topic", gt.Name)
			}
			topic.GiveAccess(owner, types.ModeCFull, types.ModeCFull)
		}
		topic.CreatedAt = getCreatedTime(gt.CreatedAt)

		if err = store.Topics.Create(topic, owner, gt.OwnerPrivate); err != nil {
			log.Fatal(err)
		}
		fmt.Println("grp;" + gt.Name + ";" + name)
	}

	log.Println("Generating P2P subscriptions...")

	for i, ss := range data.P2psubs {
		if ss.Users[0].Name < ss.Users[1].Name {
			ss.pair = ss.Users[0].Name + ":" + ss.Users[1].Name
		} else {
			ss.pair = ss.Users[1].Name + ":" + ss.Users[0].Name
		}

		uid1 := types.ParseUid(nameIndex[ss.Users[0].Name])
		uid2 := types.ParseUid(nameIndex[ss.Users[1].Name])
		topic := uid1.P2PName(uid2)
		created := getCreatedTime(ss.CreatedAt)

		// Assign default access mode
		s0want := types.ModeCP2P
		s0given := types.ModeCP2P
		s1want := types.ModeCP2P
		s1given := types.ModeCP2P

		// Check of non-default access mode was provided
		if ss.Users[0].Want != "" {
			if err := s0want.UnmarshalText([]byte(ss.Users[0].Want)); err != nil {
				log.Fatal(err)
			}
		}
		if ss.Users[0].Have != "" {
			if err := s0given.UnmarshalText([]byte(ss.Users[0].Have)); err != nil {
				log.Fatal(err)
			}
		}
		if ss.Users[1].Want != "" {
			if err := s1want.UnmarshalText([]byte(ss.Users[1].Want)); err != nil {
				log.Fatal(err)
			}
		}
		if ss.Users[1].Have != "" {
			if err := s1given.UnmarshalText([]byte(ss.Users[1].Have)); err != nil {
				log.Fatal(err)
			}
		}

		err := store.Topics.CreateP2P(
			&types.Subscription{
				ObjHeader: types.ObjHeader{CreatedAt: created},
				User:      uid1.String(),
				Topic:     topic,
				ModeWant:  s0want,
				ModeGiven: s0given,
				Private:   ss.Users[0].Private},
			&types.Subscription{
				ObjHeader: types.ObjHeader{CreatedAt: created},
				User:      uid2.String(),
				Topic:     topic,
				ModeWant:  s1want,
				ModeGiven: s1given,
				Private:   ss.Users[1].Private})

		if err != nil {
			log.Fatal(err)
		}

		data.P2psubs[i].pair = ss.pair
		nameIndex[ss.pair] = topic
		fmt.Println("p2p;" + ss.pair + ";" + topic)
	}

	log.Println("Generating group subscriptions...")

	for _, ss := range data.Groupsubs {

		want := types.ModeCPublic
		given := types.ModeCPublic
		if ss.Want != "" {
			if err := want.UnmarshalText([]byte(ss.Want)); err != nil {
				log.Fatal(err)
			}
		}
		if ss.Have != "" {
			if err := given.UnmarshalText([]byte(ss.Have)); err != nil {
				log.Fatal(err)
			}
		}

		if err = store.Subs.Create(&types.Subscription{
			ObjHeader: types.ObjHeader{CreatedAt: getCreatedTime(ss.CreatedAt)},
			User:      nameIndex[ss.User],
			Topic:     nameIndex[ss.Topic],
			ModeWant:  want,
			ModeGiven: given,
			Private:   ss.Private}); err != nil {

			log.Fatal(err)
		}
	}

	seqIds := map[string]int{}
	now := types.TimeNow().Add(-time.Minute * 10)

	messageCount := len(data.Messages)
	if messageCount > 0 {
		log.Println("Inserting messages...")

		if messageCount > 1 {
			// Shuffle messages
			rand.Shuffle(len(data.Messages), func(i, j int) {
				data.Messages[i], data.Messages[j] = data.Messages[j], data.Messages[i]
			})

			// Starting 4 days ago.
			timestamp := now.Add(time.Hour * time.Duration(-24*4))
			toInsert := 96 // 96 is the maximum, otherwise messages may appear in the future
			// Initial maximum increment of the message sent time in milliseconds
			increment := 3600 * 1000
			subIdx := rand.Intn(len(data.Groupsubs) + len(data.P2psubs)*2)
			for i := 0; i < toInsert; i++ {
				// At least 20% of subsequent messages should come from the same user in the same topic.
				if rand.Intn(5) > 0 {
					subIdx = rand.Intn(len(data.Groupsubs) + len(data.P2psubs)*2)
				}

				var topic string
				var from types.Uid
				if subIdx < len(data.Groupsubs) {
					topic = nameIndex[data.Groupsubs[subIdx].Topic]
					from = types.ParseUid(nameIndex[data.Groupsubs[subIdx].User])
				} else {
					idx := (subIdx - len(data.Groupsubs)) / 2
					usr := (subIdx - len(data.Groupsubs)) % 2
					sub := data.P2psubs[idx]
					topic = nameIndex[sub.pair]
					from = types.ParseUid(nameIndex[sub.Users[usr].Name])
				}

				seqIds[topic]++
				seqId := seqIds[topic]
				str := data.Messages[i%len(data.Messages)]
				// Max time between messages is 2 hours, averate - 1 hour, time is increasing as seqId increases
				timestamp = timestamp.Add(time.Microsecond * time.Duration(rand.Intn(increment)))
				if err = store.Messages.Save(&types.Message{
					ObjHeader: types.ObjHeader{CreatedAt: timestamp},
					SeqId:     seqId,
					Topic:     topic,
					From:      from.String(),
					Content:   str}, true); err != nil {
					log.Fatal("Failed to insert message: ", err)
				}

				// New increment: remaining time until 'now' divided by the number of messages to be inserted,
				// then converted to milliseconds.
				increment = int(now.Sub(timestamp).Nanoseconds() / int64(toInsert-i) / 1000000)

				// log.Printf("Msg.seq=%d at %v, topic='%s' from='%s'", msg.SeqId, msg.CreatedAt, topic, from.UserId())
			}
		} else {
			// Only one message is provided. Just insert it into every topic.
			now := time.Now().UTC().Add(-time.Minute).Round(time.Millisecond)

			for _, gt := range data.Grouptopics {
				seqIds[nameIndex[gt.Name]] = 1
				if err = store.Messages.Save(&types.Message{
					ObjHeader: types.ObjHeader{CreatedAt: now},
					SeqId:     1,
					Topic:     nameIndex[gt.Name],
					From:      nameIndex[gt.Owner],
					Content:   data.Messages[0]}, true); err != nil {

					log.Fatal("Failed to insert message: ", err)
				}
			}

			usedp2p := make(map[string]bool)
			for i := 0; len(usedp2p) < len(data.P2psubs)/2; i++ {
				sub := data.P2psubs[i]
				if usedp2p[nameIndex[sub.pair]] {
					continue
				}
				usedp2p[nameIndex[sub.pair]] = true
				seqIds[nameIndex[sub.pair]] = 1
				if err = store.Messages.Save(&types.Message{
					ObjHeader: types.ObjHeader{CreatedAt: now},
					SeqId:     1,
					Topic:     nameIndex[sub.pair],
					From:      nameIndex[sub.Users[0].Name],
					Content:   data.Messages[0]}, true); err != nil {

					log.Fatal("Failed to insert message: ", err)
				}
			}
		}
	}

	if len(data.Forms) != 0 {
		from := nameIndex[botAccount]
		log.Println("Inserting forms as ", botAccount, from)
		ts := now
		for _, form := range data.Forms {
			for _, sub := range data.P2psubs {
				ts = ts.Add(time.Second)
				seqIds[nameIndex[sub.pair]]++
				seqId := seqIds[nameIndex[sub.pair]]
				if sub.Users[0].Name == botAccount || sub.Users[1].Name == botAccount {
					if err = store.Messages.Save(&types.Message{
						ObjHeader: types.ObjHeader{CreatedAt: ts},
						SeqId:     seqId,
						Topic:     nameIndex[sub.pair],
						Head:      types.MessageHeaders{"mime": "text/x-drafty"},
						From:      from,
						Content:   form}, true); err != nil {
						log.Fatal("Failed to insert form: ", err)
					}
				}
			}
		}
	}

	log.Println("All done.")
}

// Go json cannot unmarshal Duration from a string, thus this hack.
func getCreatedTime(delta string) time.Time {
	dd, err := time.ParseDuration(delta)
	if err != nil && delta != "" {
		log.Fatal("Invalid duration string", delta)
	}
	return time.Now().UTC().Round(time.Millisecond).Add(dd)
}

type photoStruct struct {
	Type string `json:"type" db:"type"`
	Data []byte `json:"data" db:"data"`
}

type vcard struct {
	Fn    string       `json:"fn" db:"fn"`
	Photo *photoStruct `json:"photo,omitempty" db:"photo"`
}

// {"fn": "Alice Johnson", "photo": "alice-128.jpg"}
func parsePublic(public *vCardy, path string) *vcard {
	var photo *photoStruct
	var err error

	if public.Fn == "" && public.Photo == "" {
		return nil
	}

	fname := public.Photo
	if fname != "" {
		photo = &photoStruct{Type: public.Type}
		dir, _ := filepath.Split(fname)
		if dir == "" {
			dir = path
		}
		photo.Data, err = ioutil.ReadFile(filepath.Join(dir, fname))
		if err != nil {
			log.Fatal(err)
		}
	}

	return &vcard{Fn: public.Fn, Photo: photo}
}
