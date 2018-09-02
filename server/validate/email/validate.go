// This email validator can send email through an SMTP server

package email

import (
	"bytes"
	"encoding/json"
	ht "html/template"
	"log"
	"math/rand"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tinode/chat/server/store"
	t "github.com/tinode/chat/server/store/types"
)

// Validator configuration.
type validator struct {
	TemplateFile   string `json:"msg_body_templ"`
	Subject        string `json:"msg_subject"`
	SendFrom       string `json:"sender"`
	SenderPassword string `json:"sender_password"`
	DebugResponse  string `json:"debug_response"`
	MaxRetries     int    `json:"max_retries"`
	SMTPAddr       string `json:"smtp_server"`
	SMTPPort       string `json:"smtp_port"`
	htmlTempl      *ht.Template
	auth           smtp.Auth
}

const (
	maxRetries  = 4
	defaultPort = "25"

	// Technically email could be up to 255 bytes long but practically 128 is enough.
	maxEmailLength = 128

	// codeLength = log10(maxCodeValue)
	codeLength   = 6
	maxCodeValue = 1000000
)

// Init: initialize validator.
func (v *validator) Init(jsonconf string) error {
	var err error
	if err = json.Unmarshal([]byte(jsonconf), v); err != nil {
		return err
	}

	// SendFrom could be an RFC 5322 address of the form "John Doe <jdoe@example.com>". Parse it.
	if sender, err := mail.ParseAddress(v.SendFrom); err == nil {
		v.auth = smtp.PlainAuth("", sender.Address, v.SenderPassword, v.SMTPAddr)
	} else {
		return err
	}

	// If a relative path is provided, try to resolve it relative to the exec file location,
	// not whatever directory the user is in.
	if !filepath.IsAbs(v.TemplateFile) {
		basepath, err := os.Executable()
		if err == nil {
			v.TemplateFile = filepath.Join(filepath.Dir(basepath), v.TemplateFile)
		}
	}

	v.htmlTempl, err = ht.ParseFiles(v.TemplateFile)
	if err != nil {
		return err
	}

	// Initialize random number generator.
	rand.Seed(time.Now().UnixNano())

	if v.MaxRetries == 0 {
		v.MaxRetries = maxRetries
	}
	if v.SMTPPort == "" {
		v.SMTPPort = defaultPort
	}

	return nil
}

// PreCheck validates the credential and parameters without sending an email.
func (v *validator) PreCheck(cred string, params interface{}) error {
	if len(cred) > maxEmailLength {
		return t.ErrMalformed
	}

	_, err := mail.ParseAddress(cred)
	if err != nil {
		return t.ErrMalformed
	}

	// TODO: Check email uniqueness
	return nil
}

// Send a request for confirmation to the user: makes a record in DB  and nothing else.
func (v *validator) Request(user t.Uid, email, lang string, params interface{}, resp string) error {

	// Email validator cannot accept an immmediate response.
	if resp != "" {
		return t.ErrFailed
	}

	// Generate expected response as a random numeric string between 0 and 999999
	resp = strconv.FormatInt(int64(rand.Intn(maxCodeValue)), 10)
	resp = strings.Repeat("0", codeLength-len(resp)) + resp

	body := new(bytes.Buffer)
	if err := v.htmlTempl.Execute(body, map[string]interface{}{"Code": resp}); err != nil {
		return err
	}

	// Send email without blocking. Email sending may take long time.
	go v.send(email, "Confirm email", string(body.Bytes()))

	return store.Users.SaveCred(&t.Credential{
		User:   user.String(),
		Method: "email",
		Value:  email,
		Resp:   resp,
	})
}

// Find if user exists in the database, and if so return OK.
func (v *validator) Check(user t.Uid, resp string) error {
	cred, err := store.Users.GetCred(user, "email")
	if err != nil {
		return err
	}

	if cred == nil {
		// Request to validate non-existent credential.
		return t.ErrNotFound
	}

	if cred.Retries > v.MaxRetries {
		return t.ErrPolicy
	}
	if resp == "" {
		return t.ErrCredentials
	}

	// Comparing with dummy response too.
	if cred.Resp == resp || v.DebugResponse == resp {
		// Valid response, save confirmation.
		return store.Users.ConfirmCred(user, "email")
	}

	// Invalid response, increment fail counter.
	store.Users.FailCred(user, "email")

	return t.ErrCredentials
}

// Delete deletes user's records.
func (v *validator) Delete(user t.Uid) error {
	return nil
}

// This is a basic SMTP sender which connects to Gmail using login/password.
// -
// See here how to send email from Amazon SES:
// https://docs.aws.amazon.com/sdk-for-go/api/service/ses/#example_SES_SendEmail_shared00
// -
// Here are instructions for Google cloud:
// https://cloud.google.com/appengine/docs/standard/go/mail/sending-receiving-with-mail-api
func (v *validator) send(to, subj, body string) error {
	err := smtp.SendMail(v.SMTPAddr+":"+v.SMTPPort, v.auth, v.SendFrom, []string{to},
		[]byte("To: "+to+
			"\nSubject: "+
			subj+
			"\nMIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"+
			body))
	log.Println("Email validator error", err)

	return err
}

func init() {
	store.RegisterValidator("email", &validator{})
}
