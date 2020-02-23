// Package validate defines an interface which must be implmented by credential validators.
package validate

import (
	t "github.com/tinode/chat/server/store/types"
)

// Validator handles validation of user's credentials, like email or phone.
type Validator interface {
	// Init initializes the validator.
	Init(jsonconf string) error

	// PreCheck pre-validates the credential without sending an actual request for validation:
	// check uniqueness (if appropriate), format, etc
	PreCheck(cred string, params interface{}) error

	// Request sends a request for confirmation to the user. Returns true if it's a new credential,
	// false if it re-sent request for an existing unconfirmed credential.
	//   user: UID of the user making the request.
	//   cred: credential being validated, such as email or phone.
	//   lang: user's human language as repored in the session.
	//   resp: optional response if user already has it (i.e. captcha/recaptcha).
	//   tmpToken: temporary authentication token to include in the request.
	Request(user t.Uid, cred, lang, resp string, tmpToken []byte) (bool, error)

	// ResetSecret sends a message with instructions for resetting an authentication secret.
	//   cred: address to use for the message.
	//   scheme: authentication scheme being reset.
	//   lang: human language as reported in the session.
	//   tmpToken: temporary authentication token
	//   params: authentication params.
	ResetSecret(cred, scheme, lang string, tmpToken []byte, params map[string]interface{}) error

	// Check checks validity of user's response.
	// Returns the value of validated credential on success.
	Check(user t.Uid, resp string) (string, error)

	// Remove deletes or deactivates user's given value.
	Remove(user t.Uid, value string) error

	// Delete deletes user's record.
	Delete(user t.Uid) error
}
