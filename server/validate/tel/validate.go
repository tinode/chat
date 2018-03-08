package tel

import (
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// Empty placeholder struct.
type validator struct{}

// Init is a noop.
func (validator) Init(jsonconf string) error {
	return nil
}

// Send a request for confirmation to the user: makes a record in DB  and nothing else.
func (validator) Request(user t.Uid, cred string, params map[string]interface{}) error {
	// TODO: actually send a validation SMS or make a call to the provided `cred` here.
}

// Find if user exists in the database, and if so return OK. Any response is accepted.
func (validator) Confirm(resp string) (t.Uid, error) {
	// TODO: check response against a database.
}

func init() {
	store.RegisterValidator("tel", &validator{})
}
