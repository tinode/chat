package tests

import (
	"time"

	"github.com/tinode/chat/server/store/types"
)

var users []types.User

func initUsers() {
	now := time.Now().Round(time.Millisecond).UTC()

	users = append(users, types.User{
		ObjHeader: types.ObjHeader{
			Id:        "02TvNSWWktw",
			CreatedAt: now.Add(-140 * time.Minute),
			UpdatedAt: now.Add(-140 * time.Minute),
		},
		State:       1,
		Access:      types.DefaultAccess{},
		LastSeen:    &now,
		UserAgent:   "SomeAgent v1.2.3",
		Tags:        []string{"alice"},
	})
	users = append(users, types.User{
		ObjHeader: types.ObjHeader{
			Id:        "4Og8ARhtBWA",
			CreatedAt: now.Add(-130 * time.Minute),
			UpdatedAt: now.Add(-130 * time.Minute),
		},
		State:       1,
		Access:      types.DefaultAccess{},
		LastSeen:    &now,
		UserAgent:   "Tinode Web v111.222.333",
		Tags:        []string{"bob"},
	})
	deletedAt := now.Add(-100 * time.Minute)
	users = append(users, types.User{
		ObjHeader: types.ObjHeader{
			Id:        "07ZtlTZfaXo",
			CreatedAt: now.Add(-130 * time.Minute),
			UpdatedAt: now.Add(-130 * time.Minute),
			DeletedAt: &deletedAt,
		},
		State:       1,
		Access:      types.DefaultAccess{},
		LastSeen:    &now,
		UserAgent:   "Tindroid v1.2.3",
		Tags:        []string{"carol"},
	})
}

func initData() {
	initUsers()
}
