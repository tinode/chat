package tests

import (
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store/types"
)

type authRecord struct {
	Id      string `bson:"_id"`
	UserId  string
	Scheme  string
	AuthLvl auth.Level
	Secret  []byte
	Expires time.Time
}

var uGen types.UidGenerator
var users []*types.User
var creds []*types.Credential
var recs []authRecord
var topics []*types.Topic
var subs []*types.Subscription
var msgs []*types.Message
var devs []*types.DeviceDef
var files []*types.FileDef
var now time.Time

func initUsers() {
	users = append(users, &types.User{
		ObjHeader: types.ObjHeader{
			Id: "3ysxkod5hNM",
		},
		UserAgent: "SomeAgent v1.2.3",
		Tags:      []string{"alice"},
	})
	users = append(users, &types.User{
		ObjHeader: types.ObjHeader{
			Id: "9AVDamaNCRY",
		},
		UserAgent: "Tinode Web v111.222.333",
		Tags:      []string{"bob"},
	})
	users = append(users, &types.User{
		ObjHeader: types.ObjHeader{
			Id: "xQLrX3WPS2o",
		},
		UserAgent: "Tindroid v1.2.3",
		Tags:      []string{"carol"},
	})
	for _, user := range users {
		user.InitTimes()
	}
	deletedAt := now.Add(10 * time.Minute)
	users[2].State = types.StateDeleted
	users[2].StateAt = &deletedAt
}
func initCreds() {
	creds = append(creds, &types.Credential{ // 0
		User:   users[0].Id,
		Method: "email",
		Value:  "alice@test.example.com",
		Done:   true,
	})
	creds = append(creds, &types.Credential{ // 1
		User:   users[1].Id,
		Method: "email",
		Value:  "bob@test.example.com",
		Done:   true,
	})
	creds = append(creds, &types.Credential{ // 2
		User:   users[1].Id,
		Method: "email",
		Value:  "bob@test.example.com",
	})
	creds = append(creds, &types.Credential{ // 3
		User:   users[2].Id,
		Method: "tel",
		Value:  "+998991112233",
	})
	creds = append(creds, &types.Credential{ // 4
		User:   users[2].Id,
		Method: "tel",
		Value:  "+998993332211",
		Done:   true,
	})
	creds = append(creds, &types.Credential{ // 5
		User:   users[2].Id,
		Method: "email",
		Value:  "asdf@example.com",
	})
	for _, cred := range creds {
		cred.InitTimes()
	}
	creds[3].CreatedAt = now.Add(-10 * time.Minute)
	creds[3].UpdatedAt = now.Add(-10 * time.Minute)
}
func initAuthRecords() {
	recs = append(recs, authRecord{
		Id:      "basic:alice",
		UserId:  users[0].Id,
		Scheme:  "basic",
		AuthLvl: auth.LevelAuth,
		Secret:  []byte{'a', 'l', 'i', 'c', 'e'},
		Expires: now.Add(24 * time.Hour),
	})
	recs = append(recs, authRecord{
		Id:      "basic:bob",
		UserId:  users[1].Id,
		Scheme:  "basic",
		AuthLvl: auth.LevelAuth,
		Secret:  []byte{'b', 'o', 'b'},
		Expires: now.Add(24 * time.Hour),
	})
}
func initTopics() {
	topics = append(topics, &types.Topic{
		ObjHeader: types.ObjHeader{
			Id:        "grpgRXf0rU4uR4",
			CreatedAt: now.Add(10 * time.Minute),
			UpdatedAt: now,
		},
		TouchedAt: now,
		Owner:     users[0].Id,
		SeqId:     111,
		Tags:      []string{"travel", "zxcv"},
	})
	topics = append(topics, &types.Topic{
		ObjHeader: types.ObjHeader{
			Id:        "p2p9AVDamaNCRbfKzGSh3mE0w",
			CreatedAt: now,
			UpdatedAt: now,
		},
		TouchedAt: now,
		SeqId:     12,
	})
	topics = append(topics, &types.Topic{
		ObjHeader: types.ObjHeader{
			Id:        "p2pxQLrX3WPS2rfKzGSh3mE0w",
			CreatedAt: now,
			UpdatedAt: now,
		},
		TouchedAt: now,
		SeqId:     15,
	})
	topics = append(topics, &types.Topic{
		ObjHeader: types.ObjHeader{
			Id:        "p2pE1iE7I9JN5ESv44HiLbj1A",
			CreatedAt: now,
			UpdatedAt: now,
		},
		TouchedAt: now,
		SeqId:     555,
		Tags:      []string{"qwer"},
	})
	topics = append(topics, &types.Topic{
		ObjHeader: types.ObjHeader{
			Id:        "p2pQvr1xwKU01LfKzGSh3mE0w",
			CreatedAt: now,
			UpdatedAt: now,
		},
		TouchedAt: now,
		SeqId:     333,
		Tags:      []string{"asdf"},
	})
}
func initSubs() {
	subs = append(subs, &types.Subscription{
		ObjHeader: types.ObjHeader{
			CreatedAt: now,
			UpdatedAt: now.Add(10 * time.Minute),
		},
		User:      users[0].Id,
		Topic:     topics[0].Id,
		RecvSeqId: 5,
		ReadSeqId: 1,
		ModeWant:  255,
		ModeGiven: 255,
	})
	subs = append(subs, &types.Subscription{
		ObjHeader: types.ObjHeader{
			CreatedAt: now,
			UpdatedAt: now.Add(15 * time.Minute),
		},
		User:      users[1].Id,
		Topic:     topics[0].Id,
		RecvSeqId: 6,
		ReadSeqId: 3,
		ModeWant:  47,
		ModeGiven: 47,
	})
	subs = append(subs, &types.Subscription{
		ObjHeader: types.ObjHeader{
			CreatedAt: now.Add(-10 * time.Hour),
			UpdatedAt: now.Add(-10 * time.Hour),
		},
		User:      users[0].Id,
		Topic:     topics[1].Id,
		RecvSeqId: 9,
		ReadSeqId: 5,
		ModeWant:  47,
		ModeGiven: 47,
	})
	subs = append(subs, &types.Subscription{
		ObjHeader: types.ObjHeader{
			CreatedAt: now,
			UpdatedAt: now.Add(20 * time.Minute),
		},
		User:      users[1].Id,
		Topic:     topics[1].Id,
		RecvSeqId: 9,
		ReadSeqId: 5,
		ModeWant:  47,
		ModeGiven: 47,
	})
	subs = append(subs, &types.Subscription{
		ObjHeader: types.ObjHeader{
			CreatedAt: now,
			UpdatedAt: now.Add(30 * time.Minute),
		},
		User:      users[2].Id,
		Topic:     topics[2].Id,
		RecvSeqId: 0,
		ReadSeqId: 0,
		ModeWant:  47,
		ModeGiven: 47,
	})
	subs = append(subs, &types.Subscription{
		ObjHeader: types.ObjHeader{
			CreatedAt: now,
			UpdatedAt: now.Add(40 * time.Minute),
		},
		User:      users[2].Id,
		Topic:     topics[3].Id,
		RecvSeqId: 555,
		ReadSeqId: 455,
		ModeWant:  47,
		ModeGiven: 47,
	})
	for _, sub := range subs {
		sub.SetTouchedAt(now)
	}
}
func initMessages() {
	msgs = append(msgs, &types.Message{
		SeqId:   1,
		Topic:   topics[0].Id,
		From:    users[0].Id,
		Content: "msg1",
	})
	msgs = append(msgs, &types.Message{
		SeqId:   2,
		Topic:   topics[0].Id,
		From:    users[2].Id,
		Content: "msg2",
		DeletedFor: []types.SoftDelete{{
			User:  users[0].Id,
			DelId: 1}},
	})
	msgs = append(msgs, &types.Message{
		SeqId:   3,
		Topic:   topics[0].Id,
		From:    users[0].Id,
		Content: "msg31",
	})
	msgs = append(msgs, &types.Message{
		SeqId:   1,
		Topic:   topics[1].Id,
		From:    users[1].Id,
		Content: "msg1",
	})
	msgs = append(msgs, &types.Message{
		SeqId:   5,
		Topic:   topics[1].Id,
		From:    users[1].Id,
		Content: "msg2",
	})
	msgs = append(msgs, &types.Message{
		SeqId:   11,
		Topic:   topics[1].Id,
		From:    users[0].Id,
		Content: "msg3",
	})

	for _, msg := range msgs {
		msg.InitTimes()
		msg.SetUid(uGen.Get())
	}
}
func initDevices() {
	devs = append(devs, &types.DeviceDef{
		DeviceId: "2934ujfoviwj09ntf094",
		Platform: "Android",
		LastSeen: now,
		Lang:     "en_EN",
	})
	devs = append(devs, &types.DeviceDef{
		DeviceId: "pogpjb023b09gfdmp",
		Platform: "iOS",
		LastSeen: now,
		Lang:     "en_EN",
	})
}
func initFileDefs() {
	files = append(files, &types.FileDef{
		ObjHeader: types.ObjHeader{
			Id:        uGen.GetStr(),
			CreatedAt: now,
			UpdatedAt: now,
		},
		Status:   types.UploadStarted,
		User:     users[0].Id,
		MimeType: "application/pdf",
		Location: "uploads/qwerty.pdf",
	})
	files = append(files, &types.FileDef{
		ObjHeader: types.ObjHeader{
			Id:        uGen.GetStr(),
			CreatedAt: now.Add(2 * time.Minute),
			UpdatedAt: now.Add(2 * time.Minute),
		},
		Status:   types.UploadStarted,
		User:     users[0].Id,
		Location: "uploads/asdf.txt",
	})
}
func initData() {
	// Use fixed timestamp to make tests more predictable
	now = time.Date(2021, time.June, 12, 11, 39, 24, 15, time.Local).UTC().Round(time.Millisecond)
	initUsers()
	initCreds()
	initAuthRecords()
	initTopics()
	initSubs()
	initMessages()
	initDevices()
	initFileDefs()
}
