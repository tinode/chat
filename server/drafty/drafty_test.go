package drafty

import (
	"encoding/json"
	"testing"
)

var validInputs = []string{
	`"This is a plain text string."`,
	`{
		"txt":"This is a string with a line break.",
		"fmt":[{"at":9,"tp":"BR"}]
	}`,
	`{
		"ent":[{"data":{"mime":"image/jpeg","name":"hello.jpg","val":"<38992, bytes: ...>","width":100, "height":80},"tp":"EX"}],
		"fmt":[{"at":-1, "key":0}]
	}`,
	`{
		"ent":[{"data":{"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"},"tp":"LN"}],
		"fmt":[{"len":22}],
		"txt":"https://api.tinode.co/"
	}`,
	`{
		"ent":[{"data":{"url":"https://api.tinode.co/"},"tp":"LN"}],
		"fmt":[{"len":22}],
		"txt":"https://api.tinode.co/"
	}`,
	`{
		"ent":[{"data":{"url":"http://tinode.co"},"tp":"LN"}],
		"fmt":[{"at":9,"len":3}, {"at":4,"len":3}],
		"txt":"Url one, two"
	}`,
	`{
		"ent":[{"data":{"height":213,"mime":"image/jpeg","name":"roses.jpg","val":"<38992, bytes: ...>","width":638},"tp":"IM"}],
		"fmt":[{"len":1}],
		"txt":" "
	}`,
	`{
		"txt":"This text has staggered formats",
		"fmt":[{"at":5,"len":8,"tp":"EM"},{"at":10,"len":13,"tp":"ST"}]
	}`,
	`{
		"txt":"This text is formatted and deleted too",
		"fmt":[{"at":5,"len":4,"tp":"ST"},{"at":13,"len":9,"tp":"EM"},{"at":35,"len":3,"tp":"ST"},{"at":27,"len":11,"tp":"DL"}]
	}`,
	`{
		"txt":"мультибайтовый юникод",
		"fmt":[{"len":14,"tp":"ST"},{"at":15,"len":6,"tp":"EM"}]
	}`,
	`{
		"txt":"Alice Johnson    This is a test",
		"fmt":[{"at":13,"len":1,"tp":"BR"},{"at":15,"len":1},{"len":13,"key":1},{"len":16,"tp":"QQ"},{"at":16,"len":1,"tp":"BR"}],
		"ent":[{"tp":"IM","data":{"mime":"image/jpeg","val":"<1292, bytes: /9j/4AAQSkZJ...rehH5o6D/9k=>","width":25,"height":14,"size":968}},{"tp":"MN","data":{"color":2}}]
	}`,
}

var invalidInputs = []string{
	`{
		"txt":"This should fail",
		"fmt":[{"at":50,"len":-45,"tp":"ST"}]
	}`,
	`{
		"txt":"This should fail",
		"fmt":[{"at":0,"len":50,"tp":"ST"}]
	}`,
	`{
		"ent":[],
		"fmt":[{"at":0,"len":1,"tp":"ST","key":1}]
	}`,
	`{
		"ent":[{"xy": true, "tp": "XY"}],
		"fmt":[{"len":1,"key":-2}],
		"txt":" "
	}`,
	`{
		"ent":[{"data": true, "tp": "ST"}],
		"fmt":[{"len":1,"key":42, "at":"33"}],
		"txt":"123"
	}`,
	`{
		"txt":true
	}`,
	`{
		"invalid":[{"data": true, "tp": "ST"}],
		"content":[{"len":1, "key":42}]
	}`,
}

func TestPlainText(t *testing.T) {
	expect := []string{
		"This is a plain text string.",
		"This is a\n string with a line break.",
		"[FILE 'hello.jpg']",
		"[https://api.tinode.co/](https://www.youtube.com/watch?v=dQw4w9WgXcQ)",
		"https://api.tinode.co/",
		"Url [one](http://tinode.co), [two](http://tinode.co)",
		"[IMAGE 'roses.jpg']",
		"This _text has_ staggered formats",
		"This *text* is _formatted_ and ~deleted *too*~",
		"*мультибайтовый* _юникод_",
		"This is a test",
	}

	for i := range validInputs {
		var val interface{}
		if err := json.Unmarshal([]byte(validInputs[i]), &val); err != nil {
			t.Errorf("Failed to parse input %d '%s': %s", i, validInputs[i], err)
		}
		res, err := PlainText(val)
		if err != nil {
			t.Errorf("%d failed with error: %s", i, err)
		} else if res != expect[i] {
			t.Errorf("%d output '%s' does not match '%s'", i, res, expect[i])
		}
	}

	for i := range invalidInputs {
		var val interface{}
		if err := json.Unmarshal([]byte(invalidInputs[i]), &val); err != nil {
			// Don't make it an error: we are not testing validity of json.Unmarshal.
			t.Logf("Failed to parse input %d '%s': %s", i, invalidInputs[i], err)
		}
		res, err := PlainText(val)
		if err == nil {
			t.Errorf("invalid input %d '%s' did not cause an error '%s'", i, invalidInputs[i], res)
		}
	}
}

func TestPreview(t *testing.T) {
	expect := []string{
		`{"txt":"This is a plain"}`,
		`{"txt":"This is a strin","fmt":[{"tp":"BR","at":9}]}`,
		`{"fmt":[{"at":-1}],"ent":[{"tp":"EX","data":{"height":80,"mime":"image/jpeg","name":"hello.jpg","width":100}}]}`,
		`{"txt":"https://api.tin","fmt":[{"len":15}],"ent":[{"tp":"LN","data":{"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}}]}`,
		`{"txt":"https://api.tin","fmt":[{"len":15}],"ent":[{"tp":"LN","data":{"url":"https://api.tinode.co/"}}]}`,
		`{"txt":"Url one, two","fmt":[{"at":4,"len":3},{"at":9,"len":3}],"ent":[{"tp":"LN","data":{"url":"http://tinode.co"}}]}`,
		`{"txt":" ","fmt":[{"len":1}],"ent":[{"tp":"IM","data":{"height":213,"mime":"image/jpeg","name":"roses.jpg","width":638}}]}`,
		`{"txt":"This text has s","fmt":[{"tp":"EM","at":5,"len":8}]}`,
		`{"txt":"This text is fo","fmt":[{"tp":"ST","at":5,"len":4},{"tp":"EM","at":13,"len":2}]}`,
		`{"txt":"мультибайтовый ","fmt":[{"tp":"ST","len":14}]}`,
		`{"txt":"This is a test"}`,
	}
	for i := range validInputs {
		var val interface{}
		if err := json.Unmarshal([]byte(validInputs[i]), &val); err != nil {
			t.Errorf("Failed to parse input %d '%s': %s", i, validInputs[i], err)
		}
		res, err := Preview(val, 15)
		if err != nil {
			t.Errorf("%d failed with error: %s", i, err)
		} else if res != expect[i] {
			t.Errorf("%d output '%s' does not match '%s'", i, res, expect[i])
		}
	}

	// Only some invalid input should fail these tests.
	testsToFail := []int{3, 4, 5, 6}
	for _, i := range testsToFail {
		var val interface{}
		if err := json.Unmarshal([]byte(invalidInputs[i]), &val); err != nil {
			// Don't make it an error: we are not testing validity of json.Unmarshal.
			t.Logf("Failed to parse input %d '%s': %s", i, invalidInputs[i], err)
		}
		res, err := Preview(val, 15)
		if err == nil {
			t.Errorf("invalid input %d did not cause an error '%s'", testsToFail[i], res)
		}
	}
}
