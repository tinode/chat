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

	// Request sends a request for confirmation to the user.
	// 	user: UID of the user making the request.
	// 	cred: credential being validated, such as email or phone.
	//  lang: user's human language as repored in the session.
	// 	params: optional parameters dependent on the validation method, such as use of SMS or voice call
	//  resp: optional response if user already has it (i.e. captcha/recaptcha).
	Request(user t.Uid, cred, lang string, params interface{}, resp string) error

	// Check checks validity of user response.
	Check(user t.Uid, resp string) error

	// Delete deletes user's records.
	Delete(user t.Uid) error
}
