package tel

import (
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// Empty placeholder struct.
type validator struct{}

// Init is a noop.
func (*validator) Init(jsonconf string) error {
	return nil
}

// PreCheck validates the credential and parameters without sending an SMS or making the call.
func (*validator) PreCheck(cred string, params interface{}) error {
	// TODO: Check phone format. Format phone for E.164
	// TODO: Check phone uniqueness
	return nil
}

// Request sends a request for confirmation to the user: makes a record in DB  and nothing else.
func (*validator) Request(user t.Uid, cred, lang, resp string, tmpToken []byte) error {
	// TODO: actually send a validation SMS or make a call to the provided `cred` here.
	return nil
}

// ResetSecret sends a message with instructions for resetting an authentication secret.
func (*validator) ResetSecret(cred, scheme, lang string, tmpToken []byte) error {
	// TODO: send SMS with rest instructions.
	return nil
}

// Check checks validity of user's response.
func (*validator) Check(user t.Uid, resp string) (string, error) {
	// TODO: check response against a database.
	return "", nil
}

// Delete deletes user's records. Returns deleted credentials.
func (*validator) Delete(user t.Uid) error {
	return nil
}

// Remove or disable the given record
func (*validator) Remove(user t.Uid, value string) error {
	return nil
}

func init() {
	store.RegisterValidator("tel", &validator{})
}
