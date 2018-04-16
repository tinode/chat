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

func genDb(reset bool, dbsource string, data *Data) {
	var err error

	defer store.Close()

	log.Println("Initializing DB...")

	err = store.InitDb(dbsource, reset)
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
			State: uu.State,
			Access: types.DefaultAccess{
				Auth: types.ModeCP2P,
				Anon: types.ModeNone,
			},
			Public: parsePublic(&uu.Public, data.datapath),
		}
		user.CreatedAt = getCreatedTime(uu.CreatedAt)

		if uu.Email != "" || uu.Tel != "" {
			user.Tags = make([]string, 0)
			if uu.Email != "" {
				user.Tags = append(user.Tags, "email:"+uu.Email)
			}
			if uu.Tel != "" {
				user.Tags = append(user.Tags, "tel:"+uu.Tel)
			}
		}

		// store.Users.Create will subscribe user to !me topic but won't create a !me topic
		if _, err := store.Users.Create(&user, uu.Private); err != nil {
			log.Fatal(err)
		}

		// Save credentials: email and phone number as if they were confirmed.
		if uu.Email != "" {
			if err := store.Users.SaveCred(&types.Credential{
				User:   user.Id,
				Method: "email",
				Value:  uu.Email,
				Done:   true,
			}); err != nil {
				log.Fatal(err)
			}
		}
		if uu.Tel != "" {
			if err := store.Users.SaveCred(&types.Credential{
				User:   user.Id,
				Method: "tel",
				Value:  uu.Tel,
				Done:   true,
			}); err != nil {
				log.Fatal(err)
			}
		}

		// Add authentication record
		authHandler := store.GetAuthHandler("basic")
		passwd := uu.Password
		if passwd == "(random)" {
			// Generate random password
			passwd = getPassword(8)
		}
		if _, err := authHandler.AddRecord(&auth.Rec{Uid: user.Uid()},
			[]byte(uu.Username+":"+passwd)); err != nil {

			log.Fatal(err)
		}
		nameIndex[uu.Username] = user.Id

		// Add address book as fnd.private
		if uu.AddressBook != nil && len(uu.AddressBook) > 0 {
			if err := store.Subs.Update(user.Uid().FndName(), user.Uid(),
				map[string]interface{}{"Private": uu.AddressBook}, true); err != nil {

				log.Fatal(err)
			}
		}

		fmt.Println("usr;" + uu.Username + ";" + user.Uid().UserId() + ";" + passwd)
	}

	log.Println("Generating group topics...")

	for _, gt := range data.Grouptopics {
		name := genTopicName()
		nameIndex[gt.Name] = name

		topic := &types.Topic{
			ObjHeader: types.ObjHeader{Id: name},
			Access: types.DefaultAccess{
				Auth: types.ModeCPublic,
				Anon: types.ModeCReadOnly,
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

	log.Println("Generating messages...")

	seqIds := map[string]int{}

	// Starting 4 days ago
	timestamp := time.Now().UTC().Round(time.Millisecond).Add(time.Second * time.Duration(-3600*24*4))
	toInsert := 96 // 96 is the maximum, otherwise messages may appear in the future
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

		// log.Printf("Msg.seq=%d at %v, topic='%s' from='%s'", msg.SeqId, msg.CreatedAt, topic, from.UserId())
	}
}

// Go json cannot unmarshal Duration from a sring, thus this hask.
func getCreatedTime(delta string) time.Time {
	if dd, err := time.ParseDuration(delta); err != nil && delta != "" {
		log.Fatal("Invalid duration string", delta)
	} else {
		return time.Now().UTC().Round(time.Millisecond).Add(dd)
	}
	// Useless return: Go refuses to compile witout it
	return time.Time{}
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
