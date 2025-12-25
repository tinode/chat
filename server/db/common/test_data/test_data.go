package test_data

import (
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/db/common"
	"github.com/tinode/chat/server/store/types"
)

type TestData struct {
	UGen   *types.UidGenerator
	Users  []*types.User
	Creds  []*types.Credential
	Recs   []common.AuthRecord
	Topics []*types.Topic
	Subs   []*types.Subscription
	Msgs   []*types.Message
	Devs   []*types.DeviceDef
	Files  []*types.FileDef
	// Tags: add, remove, reset
	Tags [][]string
	Now  time.Time
}

func initUsers(now time.Time) []*types.User {
	users := make([]*types.User, 0, 3)
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
			Id: "0QLrX3WPS2o",
		},
		UserAgent: "Tindroid v1.2.3",
		Tags:      []string{"carol"},
	})
	for _, user := range users {
		// Initialize timestamps.
		user.InitTimes()
		// Assign user.id from user.Id.
		user.Uid()
	}
	deletedAt := now.Add(10 * time.Minute)
	users[2].State = types.StateDeleted
	users[2].StateAt = &deletedAt
	return users
}

func initCreds(now time.Time, users []*types.User) []*types.Credential {
	creds := make([]*types.Credential, 0, 6)
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
	return creds
}

func initAuthRecords(now time.Time, users []*types.User) []common.AuthRecord {
	recs := make([]common.AuthRecord, 0, 2)
	recs = append(recs, common.AuthRecord{
		Unique:  "basic:alice",
		UserId:  users[0].Id,
		Scheme:  "basic",
		AuthLvl: auth.LevelAuth,
		Secret:  []byte{'a', 'l', 'i', 'c', 'e'},
		Expires: now.Add(24 * time.Hour),
	})
	recs = append(recs, common.AuthRecord{
		Unique:  "basic:bob",
		UserId:  users[1].Id,
		Scheme:  "basic",
		AuthLvl: auth.LevelAuth,
		Secret:  []byte{'b', 'o', 'b'},
		Expires: now.Add(24 * time.Hour),
	})
	return recs
}

func initTopics(now time.Time, users []*types.User) []*types.Topic {
	topics := make([]*types.Topic, 0, 5)
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
		SubCnt:    2,
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
	})
	topics = append(topics, &types.Topic{
		ObjHeader: types.ObjHeader{
			Id:        "p2pQvr1xwKU01LfKzGSh3mE0w",
			CreatedAt: now,
			UpdatedAt: now,
		},
		TouchedAt: now,
		SeqId:     333,
	})
	return topics
}

func initSubs(now time.Time, users []*types.User, topics []*types.Topic) []*types.Subscription {
	subs := make([]*types.Subscription, 0, 6)
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
	return subs
}

func initMessages(users []*types.User, topics []*types.Topic) []*types.Message {
	msgs := make([]*types.Message, 0, 6)
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

	for i, msg := range msgs {
		msg.InitTimes()
		msg.SetUid(types.Uid(i + 1))
	}
	return msgs
}

func initDevices(now time.Time) []*types.DeviceDef {
	devs := make([]*types.DeviceDef, 0, 2)
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
	return devs
}

func initFileDefs(now time.Time, users []*types.User) []*types.FileDef {
	files := make([]*types.FileDef, 0, 2)
	files = append(files, &types.FileDef{
		ObjHeader: types.ObjHeader{
			CreatedAt: now,
			UpdatedAt: now,
		},
		Status:   types.UploadStarted,
		User:     users[0].Id,
		MimeType: "application/pdf",
		Location: "uploads/qwerty.pdf",
		Size:     123456,
	})
	files = append(files, &types.FileDef{
		ObjHeader: types.ObjHeader{
			CreatedAt: now.Add(60 * time.Minute),
			UpdatedAt: now.Add(60 * time.Minute),
		},
		Status:   types.UploadStarted,
		User:     users[0].Id,
		Location: "uploads/asdf.txt",
		Size:     654321,
	})
	files[0].SetUid(types.Uid(1001))
	files[1].SetUid(types.Uid(1002))
	return files
}

func initTags() [][]string {
	// Tags must be lowercase and non-repeating.
	addTags := []string{"tag1", "alice"}
	removeTags := []string{"alice", "tag1", "tag2"}
	resetTags := []string{"alice", "tag111", "tag333"}
	return [][]string{addTags, removeTags, resetTags}
}

func InitTestData() *TestData {
	// Use fixed timestamp to make tests more predictable
	var now = time.Date(2021, time.June, 12, 11, 39, 24, 15, time.Local).UTC().Round(time.Millisecond)
	var uGen = &types.UidGenerator{}
	if err := uGen.Init(11, []byte("testtesttesttest")); err != nil {
		return nil
	}
	var users = initUsers(now)
	var topics = initTopics(now, users)
	return &TestData{
		UGen:   uGen,
		Users:  users,
		Creds:  initCreds(now, users),
		Recs:   initAuthRecords(now, users),
		Topics: topics,
		Subs:   initSubs(now, users, topics),
		Msgs:   initMessages(users, topics),
		Devs:   initDevices(now),
		Files:  initFileDefs(now, users),
		Tags:   initTags(),
		Now:    now,
	}
}
