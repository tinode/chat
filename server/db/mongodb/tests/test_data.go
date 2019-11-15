package tests

import (
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store/types"
)

type AuthRecord struct {
	Id      string `bson:"_id"`
	UserId  string
	Scheme  string
	AuthLvl auth.Level
	Secret  []byte
	Expires time.Time
}

var users []*types.User
var creds []*types.Credential
var recs []AuthRecord
var topics []*types.Topic
var subs []*types.Subscription
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
	users[2].DeletedAt = &deletedAt
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
	recs = append(recs, AuthRecord{
		Id:      "basic:alice",
		UserId:  users[0].Id,
		Scheme:  "basic",
		AuthLvl: auth.LevelAuth,
		Secret:  []byte{'a', 'l', 'i', 'c', 'e'},
		Expires: now.Add(24 * time.Hour),
	})
	recs = append(recs, AuthRecord{
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
			CreatedAt: now,
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
		SeqId:     12,
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
			UpdatedAt: now,
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
			UpdatedAt: now,
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
			UpdatedAt: now,
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
			UpdatedAt: now,
		},
		User:      users[2].Id,
		Topic:     topics[2].Id,
		RecvSeqId: 9,
		ReadSeqId: 5,
		ModeWant:  47,
		ModeGiven: 47,
	})
	for _, sub := range subs {
		sub.SetTouchedAt(now)
	}
}

func initData() {
	now = types.TimeNow()
	initUsers()
	initCreds()
	initAuthRecords()
	initTopics()
	initSubs()
}
