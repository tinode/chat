// Package tel is an incomplete implementation of SMS or voice credential validator.
package tel

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	textt "text/template"

	"github.com/nyaruka/phonenumbers"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
	"github.com/tinode/chat/server/validate"
	i18n "golang.org/x/text/language"
)

// Empty placeholder struct.
type validator struct {
	// Base URL of the web client to tell clients.
	HostUrl string `json:"host_url"`
	// List of languages supported by templates.
	Languages []string `json:"languages"`
	// Path to email validation and password reset templates, either a template itself or a literal string.
	UniversalTemplFile string `json:"universal_templ"`
	// Sender address.
	Sender string `json:"sender"`
	// Debug response to accept during testing.
	DebugResponse string `json:"debug_response"`
	// Maximum number of validation retires.
	MaxRetries int `json:"max_retries"`
	// Length of secret numeric code to sent for validation.
	CodeLength int `json:"code_length"`

	// Must use index into language array instead of language tags because language.Matcher is brain damaged:
	// https://github.com/golang/go/issues/24211
	universalTempl []*textt.Template
	langMatcher    i18n.Matcher
	maxCodeValue   *big.Int
}

const (
	validatorName = "tel"

	defaultMaxRetries = 3

	// Default code length when one is not provided in the config
	defaultCodeLength = 6

	defaultSender = "Tinode"
)

func (v *validator) Init(jsonconf string) error {
	var err error

	if err = json.Unmarshal([]byte(jsonconf), v); err != nil {
		return err
	}

	if v.HostUrl, err = validate.ValidateHostURL(v.HostUrl); err != nil {
		return err
	}

	var universalPathTempl *textt.Template
	universalPathTempl, err = textt.New("universal").Parse(v.UniversalTemplFile)
	if err != nil {
		return err
	}

	if len(v.Languages) > 0 {
		v.universalTempl = make([]*textt.Template, len(v.Languages))
		var langTags []i18n.Tag
		// Find actual content templates for each defined language.
		for idx, lang := range v.Languages {
			tag, err := i18n.Parse(lang)
			if err != nil {
				return err
			}
			langTags = append(langTags, tag)
			if v.universalTempl[idx], _, err = validate.ReadTemplateFile(universalPathTempl, lang); err != nil {
				return err
			}
		}
		v.langMatcher = i18n.NewMatcher(langTags)
	} else {
		v.universalTempl = make([]*textt.Template, 1)
		// No i18n support. Use defaults.
		v.universalTempl[0], _, err = validate.ReadTemplateFile(universalPathTempl, "")
		if err != nil {
			return err
		}
	}

	if v.Sender == "" {
		v.Sender = defaultSender
	}
	if v.MaxRetries == 0 {
		v.MaxRetries = defaultMaxRetries
	}
	if v.CodeLength == 0 {
		v.CodeLength = defaultCodeLength
	}
	v.maxCodeValue = big.NewInt(0).Exp(big.NewInt(10), big.NewInt(int64(v.CodeLength)), nil)

	return nil
}

// IsInitialized returns true if the validator is initialized.
func (v *validator) IsInitialized() bool {
	return v.CodeLength > 0
}

// PreCheck validates the credential and parameters without sending an SMS or making the call.
// If credential is valid, it's formatted and prefixed with a tag namespace.
func (*validator) PreCheck(cred string, params map[string]interface{}) (string, error) {
	// Parse will try to extract the number from any text, make sure it's just the number.
	if !phonenumbers.VALID_PHONE_NUMBER_PATTERN.MatchString(cred) {
		return "", t.ErrMalformed
	}
	countryCode, ok := params["countryCode"].(string)
	if !ok {
		countryCode = "US"
	}
	number, err := phonenumbers.Parse(cred, countryCode)
	if err != nil {
		return "", t.ErrMalformed
	}
	if !phonenumbers.IsValidNumber(number) {
		return "", t.ErrMalformed
	}
	if numType := phonenumbers.GetNumberType(number); numType != phonenumbers.FIXED_LINE_OR_MOBILE &&
		numType != phonenumbers.MOBILE {
		return "", t.ErrMalformed
	}
	return validatorName + ":" + phonenumbers.Format(number, phonenumbers.E164), nil
}

// Request sends a request for confirmation to the user: makes a record in DB and nothing else.
func (v *validator) Request(user t.Uid, phone, lang, resp string, tmpToken []byte) (bool, error) {
	// Phone validator cannot accept an immediate response.
	if resp != "" {
		return false, t.ErrFailed
	}

	// Generate expected response as a random numeric string between 0 and 999999.
	code, err := rand.Int(rand.Reader, v.maxCodeValue)
	if err != nil {
		return false, err
	}
	resp = strconv.FormatInt(code.Int64(), 10)
	resp = strings.Repeat("0", v.CodeLength-len(resp)) + resp

	var template *textt.Template
	if v.langMatcher != nil {
		_, idx := i18n.MatchStrings(v.langMatcher, lang)
		template = v.universalTempl[idx]
	} else {
		template = v.universalTempl[0]
	}

	content, err := validate.ExecuteTemplate(template, nil, map[string]interface{}{
		"Code":    resp,
		"HostUrl": v.HostUrl})
	if err != nil {
		return false, err
	}

	// Create or update validation record in DB.
	isNew, err := store.Users.UpsertCred(&t.Credential{
		User:   user.String(),
		Method: validatorName,
		Value:  phone,
		Resp:   resp})
	if err != nil {
		return false, err
	}

	// Send SMS without blocking. It sending may take long time.
	go v.send(phone, content[""])

	return isNew, nil
}

// ResetSecret sends a message with instructions for resetting an authentication secret.
func (v *validator) ResetSecret(phone, scheme, lang string, code []byte, params map[string]interface{}) error {
	var template *textt.Template
	if v.langMatcher != nil {
		_, idx := i18n.MatchStrings(v.langMatcher, lang)
		template = v.universalTempl[idx]
	} else {
		template = v.universalTempl[0]
	}

	content, err := validate.ExecuteTemplate(template, nil, map[string]interface{}{
		"Code":    string(code),
		"HostUrl": v.HostUrl})
	if err != nil {
		return err
	}

	// Send SMS without blocking. It sending may take long time.
	go v.send(phone, content[""])

	return nil
}

// Check checks validity of user's response.
func (v *validator) Check(user t.Uid, resp string) (string, error) {
	cred, err := store.Users.GetActiveCred(user, validatorName)
	if err != nil {
		return "", err
	}

	if cred == nil {
		// Blank credential.
		return "", t.ErrNotFound
	}

	if cred.Retries > v.MaxRetries {
		return "", t.ErrPolicy
	}

	if resp == "" {
		return "", t.ErrCredentials
	}

	// Comparing with dummy response too.
	if cred.Resp == resp || v.DebugResponse == resp {
		// Valid response, save confirmation.
		return cred.Value, store.Users.ConfirmCred(user, validatorName)
	}

	// Invalid response, increment fail counter, ignore possible error.
	store.Users.FailCred(user, validatorName)

	return "", t.ErrCredentials
}

// Delete deletes user's records. Returns deleted credentials.
func (*validator) Delete(user t.Uid) error {
	return store.Users.DelCred(user, validatorName, "")
}

// Remove or disable the given record.
func (*validator) Remove(user t.Uid, value string) error {
	return store.Users.DelCred(user, validatorName, value)
}

// Implement sending the SMS.
func (*validator) send(to, body string) error {
	logs.Info.Println("Send SMS, To:", to, "; Text:", body)
	return nil
}

func init() {
	store.RegisterValidator(validatorName, &validator{})
}
