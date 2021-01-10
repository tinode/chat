package drafty

import (
	"encoding/json"
	"testing"
)

var validInputs = []string{
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
		"ent":[{"data":{"height":213,"mime":"image/jpeg","name":"roses.jpg","val":"<38992, bytes: ...>","width":638},"tp":"IM"}],
		"fmt":[{"len":1}],
		"txt":" "
	}`,
	`{
		"txt":"This text is formatted and deleted too",
		"fmt":[{"at":5,"len":4,"tp":"ST"},{"at":13,"len":9,"tp":"EM"},{"at":35,"len":3,"tp":"ST"},{"at":27,"len":11,"tp":"DL"}]
	}`,
	`{
		"txt":"мультибайтовый юникод",
		"fmt":[{"len":14,"tp":"ST"},{"at":15,"len":6,"tp":"EM"}]
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
		"fmt":[{"len":1,"key":42}],
		"txt":"123"
	}`,
	`{
		"txt":true
	}`,
}

func TestToPlainText(t *testing.T) {
	expect := []string{
		"[FILE 'hello.jpg']",
		"[https://api.tinode.co/](https://www.youtube.com/watch?v=dQw4w9WgXcQ)",
		"https://api.tinode.co/",
		"[IMAGE 'roses.jpg']",
		"This *text* is _formatted_ and ~deleted *too*~",
		"*мультибайтовый* _юникод_",
	}

	for i := range validInputs {
		var val interface{}
		json.Unmarshal([]byte(validInputs[i]), &val)
		res, err := ToPlainText(val)
		if err != nil {
			t.Error(err)
		} else if res != expect[i] {
			t.Errorf("%d output '%s' does not match '%s'", i, res, expect[i])
		}
	}

	for i := range invalidInputs {
		var val interface{}
		json.Unmarshal([]byte(invalidInputs[i]), &val)
		res, err := ToPlainText(val)
		if err == nil {
			t.Errorf("invalid input %d did not cause an error '%s'", i, res)
		}
	}
}

func TestPreview(t *testing.T) {
	expect := []string{
		`{"ent":[{"data":{"height":80,"mime":"image/jpeg","name":"hello.jpg","width":100},"tp":"EX"}],"fmt":[{"at":-1,"key":0}]}`,
		`{"ent":[{"tp":"LN"}],"fmt":[{"key":0,"len":22}],"txt":"https://api.tin"}`,
		`{"ent":[{"tp":"LN"}],"fmt":[{"key":0,"len":22}],"txt":"https://api.tin"}`,
		`{"ent":[{"data":{"height":213,"mime":"image/jpeg","name":"roses.jpg","width":638},"tp":"IM"}],"fmt":[{"key":0,"len":1}],"txt":" "}`,
		`{"fmt":[{"at":5,"len":4,"tp":"ST"},{"at":13,"len":9,"tp":"EM"}],"txt":"This text is fo"}`,
		`{"fmt":[{"len":14,"tp":"ST"}],"txt":"мультибайтовый "}`,
	}
	for i := range validInputs {
		var val interface{}
		json.Unmarshal([]byte(validInputs[i]), &val)
		res, err := Preview(val, 15)
		if err != nil {
			t.Error(err)
		}

		if res != expect[i] {
			t.Errorf("%d output '%s' does not match '%s'", i, res, expect[i])
		}
	}

	// Only some invalid input should fail these tests.
	testsToFail := []int{0, 3, 5}
	for _, i := range testsToFail {
		var val interface{}
		json.Unmarshal([]byte(invalidInputs[i]), &val)
		res, err := Preview(val, 15)
		if err == nil {
			t.Errorf("invalid input %d did not cause an error '%s'", testsToFail[i], res)
		}
	}
}
