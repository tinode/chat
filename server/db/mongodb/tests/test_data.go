package tests

import (
	"time"

	"github.com/tinode/chat/server/store/types"
)

var users []*types.User
var creds []*types.Credential
var now time.Time

func initUsers() {
	users = append(users, &types.User{
		ObjHeader: types.ObjHeader{
			Id: "02TvNSWWktw",
		},
		UserAgent: "SomeAgent v1.2.3",
		Tags:      []string{"alice"},
	})
	users = append(users, &types.User{
		ObjHeader: types.ObjHeader{
			Id: "4Og8ARhtBWA",
		},
		UserAgent: "Tinode Web v111.222.333",
		Tags:      []string{"bob"},
	})
	users = append(users, &types.User{
		ObjHeader: types.ObjHeader{
			Id: "07ZtlTZfaXo",
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
	deletedAt := now.Add(10 * time.Minute)
	creds[5].DeletedAt = &deletedAt
}

func initData() {
	now = types.TimeNow()
	initUsers()
	initCreds()
}
