package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/tinode/chat/server/store/types"
	//"golang.org/x/crypto/bcrypt"
	"io/ioutil"
	"log"
)

/*****************************************************************************
 * Storage schema
 *****************************************************************************
 * System-accessible tables:
 ***************************
 * 1. Customer (customer of the service)
 *****************************
 * Customer-accessible tables:
 *****************************
 * 2. Application (a customer may have multiple applications)
 * 3. Application keys (an application may have multiple API keys)
 ****************************************
 * Application/end-user-accessible tables
 ****************************************
 * 4. User (end-user)
 * 5. Inbox (a.k.a topics, a list of distinct threads/conversations)
 * 6. Messages (persistent store of messages)
 * 7. Contacts (a.k.a. ledger, address book)
 *****************************************************************************/

type Data struct {
	Users         []map[string]interface{} `json:"users"`
	Grouptopics   []map[string]interface{} `json:"grouptopics"`
	Subscriptions []map[string]interface{} `json:"subscriptions"`
	Messages      []string                 `json:"messages"`
}

var original = Data{
	/*
	   // Header shared by all stored objects
	   type ObjHeader struct {
	   	Id        Uid
	   	CreatedAt time.Time
	   	UpdatedAt time.Time
	   	DeletedAt *time.Time
	   }

	   // Stored user
	   type User struct {
	   	ObjHeader
	   	State    int // Unconfirmed, Active, etc.
	   	Username string
	   	Passhash []byte
	   	LastSeen time.Time
	   	Status   interface{}
	   	Params   interface{}
	   }
	*/

	Users: []map[string]interface{}{
		{"username": "alice", "state": 1, "passhash": "alice123", "status": map[string]interface{}{"text": "DND"},
			"public": "Alice Johnson", "email": "alice@example.com", "private": "some comment 123", "createdAt": "-140h"},
		{"username": "bob", "state": 1, "passhash": "bob123",
			"public": "Bob Smith", "email": "bob@example.com", "status": "stuff",
			"private": map[string]interface{}{"comment": "no comments :)"}, "createdAt": "-138h"},
		{"username": "carol", "state": 1, "passhash": "carol123", "status": "ho ho ho",
			"public": "Carol Xmas", "email": "carol@example.com", "private": "more stuff", "createdAt": "-136h"},
		{"username": "dave", "state": 1, "passhash": "dave123", "status": "hiding!",
			"public": "Dave Goliathsson", "email": "dave@example.com", "private": "stuff 123", "createdAt": "-134h"},
		{"username": "eve", "state": 1, "passhash": "eve123", // no status here
			"public": "Eve Adams", "email": "eve@example.com", "private": "apples?", "createdAt": "-132h"},
		{"username": "frank", "state": 2, "passhash": "frank123", "status": "singing!",
			"public": "Frank Sinatra", "email": "frank@example.com", "private": "things, not stuff", "createdAt": "-131h"}},

	/*
	   type Topic struct {
	   	ObjHeader
	   	State  int
	   	Name   string
	   	UseAcl bool
	   	Access struct { // Default access to topic, owner & system = full access
	   		User   AccessMode
	   		Anonym AccessMode
	   	}
	   	LastMessageAt *time.Time

	   	Public interface{}

	   	owner   Uid                  // first assigned owner
	   	perUser map[Uid]*perUserData // deserialized from Subscription
	   }
	*/

	Grouptopics: []map[string]interface{}{
		{"createdAt": "-128h",
			"name":    "*ABC", // Names will be replaced with random strings
			"public":  "Group ABC w Alice, Bob, Carol (owner)",
			"owner":   "carol",
			"private": "Carol's private data stash"},
		{"createdAt": "-126h",
			"name":   "*ABCDEF",
			"public": "Group ABCDEF w Alice (owner), Bob, Carol, Dave, Eve, Frank",
			"owner":  "alice"},
		{"createdAt": "-124h",
			"name":   "*BF",
			"public": "Group BF w Bob, Frank, no owner"},
	},

	/*
		type Subscription struct {
			ObjHeader
			User      string     // User who has relationship with the topic
			Topic     string     // Topic subscribed to
			ModeWant  AccessMode // Access applied for
			ModeGiven AccessMode // Granted access
			ClearedAt *time.Time // User deleted messages older than this time; TODO(gene): topic owner can hard-delete messages

			LastSeen map[string]time.Time // Last time when the user joined the topic, by device tag

			Private interface{} // User's private data associated with the subscription
		}
	*/
	Subscriptions: []map[string]interface{}{
		// P2P subscriptions
		{"createdAt": "-120h",
			"user":     "alice",
			"topic":    "bob",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P}, // No params
		{"createdAt": "-119h",
			"user":     "alice",
			"topic":    "carol",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P},
		{"createdAt": "-118h",
			"user":     "alice",
			"topic":    "dave",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P}, // no params
		{"createdAt": "-117h",
			"user":     "alice",
			"topic":    "eve",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P,
			"private":  "apples to oranges"},
		{"createdAt": "-116h",
			"user":     "alice",
			"topic":    "frank",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P, // Alice cannot see Frank's presence
			"private":  "Frank Frank Frank a->f"},
		{"createdAt": "-115h",
			"user":     "bob",
			"topic":    "alice",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P,
			"private":  "Alice Jo"},
		{"createdAt": "-114h",
			"user":     "bob",
			"topic":    "dave",
			"modeWant": types.ModeP2P, // Bob does not want to see Dave's presence
			"modeHave": types.ModeP2P,
			"private":  "Dave ?"},
		{"createdAt": "-113.5h",
			"user":     "bob",
			"topic":    "eve",
			"modeWant": types.ModePublic,
			"modeHave": types.ModeP2P,
			"private":  "Eve Adamsson"},
		{"createdAt": "-113.4h",
			"user":     "carol",
			"topic":    "alice",
			"modeWant": types.ModePubSub,
			"modeHave": types.ModeP2P,
			"private":  "Alice Joha"},
		{"createdAt": "-113h",
			"user":     "dave",
			"topic":    "alice",
			"modeWant": types.ModePublic,
			"modeHave": types.ModeP2P,
			"private":  "Alice Johnson"},
		{"createdAt": "-112.8h",
			"user":     "dave",
			"topic":    "bob",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P,
			"private":  "some private info here"},
		{"createdAt": "-112.6h",
			"user":     "eve",
			"topic":    "alice",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P,
			"private":  "Alice Johnson"},
		{"createdAt": "-112.4h",
			"user":     "eve",
			"topic":    "bob",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P,
			"private":  "123"},
		{"createdAt": "-112.2h",
			"user":     "frank",
			"topic":    "alice",
			"modeWant": types.ModeP2P,
			"modeHave": types.ModeP2P,
			"private":  "Johnson f->a"},
		// Gruop topic subscriptions below
		{"createdAt": "-112h",
			"user":     "alice",
			"topic":    "*ABC",
			"modeWant": types.ModePublic,
			"modeHave": types.ModePublic,
			"private":  "My super cool group topic"},
		{"createdAt": "-111.9h",
			"user":     "bob",
			"topic":    "*ABC",
			"modeWant": types.ModePublic,
			"modeHave": types.ModePublic,
			"private":  "Wow"},
		/* {"createdAt": nil, // Topic owner, no need to explicitly subscribe
		"user":     "carol",
		"topic":    "*ABC",
		"modeWant": types.ModeFull,
		"modeHave": types.ModeFull,
		"private":   "Ooga chaka"}, */
		/* {"createdAt": nil, // Topic owner, no need to explicitly subscribe
		"user":     "alice",
		"topic":    "*ABCDEF",
		"modeWant": types.ModeFull,
		"modeHave": types.ModeFull,
		"private":   "ooga ooga"}, */
		{"createdAt": "-111.8h",
			"user":     "bob",
			"topic":    "*ABCDEF",
			"modeWant": types.ModePublic,
			"modeHave": types.ModePublic,
			"private":  "Custom group description by Bob"},
		{"createdAt": "-111.7h",
			"user":     "carol",
			"topic":    "*ABCDEF",
			"modeWant": types.ModePublic,
			"modeHave": types.ModePublic,
			"private":  "Kirgudu"},
		{"createdAt": "-111.6h",
			"user":     "dave",
			"topic":    "*ABCDEF",
			"modeWant": types.ModePublic,
			"modeHave": types.ModePublic},
		{"createdAt": "-111.5h",
			"user":     "eve",
			"topic":    "*ABCDEF",
			"modeWant": types.ModePublic,
			"modeHave": types.ModePublic},
		{"createdAt": "-111.4h",
			"user":     "frank",
			"topic":    "*ABCDEF",
			"modeWant": types.ModePublic,
			"modeHave": types.ModePublic},
		{"createdAt": "-111.3h",
			"user":     "bob",
			"topic":    "*BF",
			"modeWant": types.ModePublic,
			"modeHave": types.ModePublic,
			"private":  "I'm not the owner"},
		{"createdAt": "-111.2h",
			"user":     "frank",
			"topic":    "*BF",
			"modeWant": types.ModePublic,
			"modeHave": types.ModePublic,
			"private":  "I'm not the owner either"},
	},

	// Messages are generated at random using these strings
	Messages: []string{
		"Caution: Do not view laser light with remaining eye.",
		"Caution: breathing may be hazardous to your health.",
		"Celebrate Hannibal Day this year. Take an elephant to lunch.",
		"Celibacy is not hereditary.",
		"Center 1127 -- It's not just a job, it's an adventure!",
		"Center meeting at 4pm in 2C-543",
		"Centran manuals are available in 2B-515.",
		"Charlie don't surf.",
		"Children are hereditary: if your parents didn't have any, neither will you.",
		"Clothes make the man. Naked people have little or no influence on society.",
		"Club sandwiches, not baby seals.",
		"Cocaine is nature's way of saying you make too much money.",
		"Cogito Ergo Spud.",
		"Cogito cogito ergo cogito sum.",
		"Colorless green ideas sleep furiously.",
		"Communication is only possible between equals.",
		"Computers are not intelligent.  They only think they are.",
		"Consistency is always easier to defend than correctness.",
		"Constants aren't.  Variables don't.  LISP does.  Functions won't.  Bytes do.",
		"Contains no kung fu, car chases or decapitations.",
		"Continental Life.  Why do you ask?",
		"Convictions cause convicts -- what you believe imprisons you.",
		"Core Error - Bus Dumped",
		"Could not open 2147478952 framebuffers.",
		"Courage is something you can never totally own or totally lose.",
		"Cowards die many times before their deaths;/The valiant never taste of death but once.",
		"Crazee Edeee, his prices are INSANE!!!",
		"Creativity is no substitute for knowing what you are doing.",
		"Creditors have much better memories than debtors.",
		"Critics are like eunuchs in a harem: they know how it's done, they've seen it done",
		"every day, but they're unable to do it themselves.  -Brendan Behan",
		"Cthulhu Saves!  ...  in case He's hungry later.",
		"Dames is like streetcars -- The oceans is full of 'em.  -Archie Bunker",
		"Dames lie about anything - just for practice.  -Raymond Chandler",
		"Damn it, i gotta get outta here!",
		"Dangerous knowledge is a little thing.",
		"It is certain",
		"It is decidedly so",
		"Without a doubt",
		"Yes definitely",
		"You may rely on it",
		"As I see it yes",
		"Most likely",
		"Outlook good",
		"Yes",
		"Signs point to yes",
		"Reply hazy try again",
		"Ask again later",
		"Better not tell you now",
		"Cannot predict now",
		"Concentrate and ask again",
		"Don't count on it",
		"My reply is no",
		"My sources say no",
		"Outlook not so good",
		"Very doubtful"},
}

/*
func passHash(password string) []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
}
*/

func _getRandomString() string {
	buf := make([]byte, 9)
	_, err := rand.Read(buf)
	if err != nil {
		panic("getRandomString: failed to generate a random string: " + err.Error())
	}
	//return base32.StdEncoding.EncodeToString(buf)
	return base64.URLEncoding.EncodeToString(buf)
}

func genTopicName() string {
	return "grp" + _getRandomString()
}

func main() {
	raw, err := ioutil.ReadFile("./data.json")
	if err != nil {
		log.Fatal(err)
	}

	var data Data
	err = json.Unmarshal(raw, &data)
	if err != nil {
		log.Fatal(err)
	}

	gen_rethink(&data)
}
