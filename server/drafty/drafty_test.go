package drafty

import (
	"encoding/json"
	"testing"
)

func TestToPlainText(t *testing.T) {
	validInputs := []string{
		`{
			"ent":[{"data":{"mime":"image/jpeg","name":"hello.jpg","val":"<38992, bytes: ...>"},"tp":"EX"}],
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
	expect := []string{
		"[FILE 'hello.jpg']",
		"[https://api.tinode.co/](https://www.youtube.com/watch?v=dQw4w9WgXcQ)",
		"https://api.tinode.co/",
		"[IMAGE 'roses.jpg']",
		"This *text* is _formatted_ and ~deleted *too*~",
		"*мультибайтовый* _юникод_",
	}

	invalidInputs := []string{
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
			"txt":true
		}`,
	}

	for i := range validInputs {
		var val interface{}
		json.Unmarshal([]byte(validInputs[i]), &val)
		res, err := ToPlainText(val)
		if err != nil {
			t.Error(err)
		}

		if res != expect[i] {
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
