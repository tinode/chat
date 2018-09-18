package push

import (
	"errors"
	"sort"
)

var unrecognizedContent = errors.New("content unrecognized")

// DraftyToPlainText converts message payload from Drafy format to string.
// If content is a string, then it's returned unchanged. If content is not recognized
// as either Drafy (as a map[string]interface{}) or string, an error is returned.
func DraftyToPlainText(content interface{}) (string, error) {
	if content == nil {
		return "", nil
	}

	var drafty map[string]interface{}

	switch data := content.(type) {
	case string:
		return data, nil
	case map[string]interface{}:
		drafty = data
	default:
		return "", unrecognizedContent
	}

	if drafty["txt"] == nil && drafty["fmt"] == nil && drafty["ent"] == nil {
		return "", unrecognizedContent
	}

	return draftyFormat(drafty)
}

	type span struct {
		tp   string
		at   int
		lng  int
		key  int
		data interface{}
	}
	
// Actually convert draft object to string. This is not a full-featured formatter.
// It only recognizes three cases: attachment, inline image, plain text.
// Examples:
// {
//		"ent":[{"data":{"mime":"image/jpeg","name":"hello.jpg","val":"<38992, bytes: ...>"},"tp":"EX"}],
//		"fmt":[{"at":-1}]
// } => `[ATTACHMENT: 'hello.jpg']`
// {
//		"ent":[{"data":{"url":"https://www.youtube.com/watch?v=V4EluGe2Qx4"},"tp":"LN"}],
// 		"fmt":[{"len":43}],
// 		"txt":"https://www.youtube.com/watch?v=V4EluGe2Qx4"
// } => `https://www.youtube.com/watch?v=V4EluGe2Qx4`
// {
//		"ent":[{"data":{"height":213,"mime":"image/jpeg","name":"roses.jpg","val":"<38992, bytes: ...>","width":638},"tp":"IM"}],
//		"fmt":[{"len":1}],
//		"txt":" "
//	} => [IMAGE: 'roses.jpg']
func draftyFormat(content map[string]interface{}) (string, error) {
	txt, txt_ok := content["txt"].(string)
	fmt, fmt_ok := content["fmt"].([]map[string]interface{})
	ent, ent_ok := content["ent"].([]map[string]interface{})

	if fmt == nil {
		if txt_ok {
			return txt, nil
		}
		return "", unrecognizedContent
	}

	var spans []*span
	for i := range fmt {
		s := &span{}
		s.tp, _ = fmt[i]["tp"].(string)
		s.at, _ = fmt[i]["at"].(int)
		s.lng, _ = fmt[i]["len"].(int)
		s.key, _ = fmt[i]["key"].(int)
		// Denormalize entities into spans.
		if s.tp == "" && ent_ok {
			s.data, _ = ent[s.key]["data"]
			s.tp, _ = ent[s.key]["tp"].(string)
		}
		if s.tp == "" && s.at == 0 && s.lng == 0 && s.key == 0 {
			return "", unrecognizedContent
		}
		spans = append(spans, s)
	}

	// Soft spans first by start index (asc) then by length (desc).
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].at == spans[j].at {
			// longer one comes first
			return spans[i].lng > spans[j].lng
		}
		return spans[i].at < spans[j].at
	})

	return forEach(txt, 0, len(txt), spans)
}

func forEach(line string, start, end int, spans []*span) (string, error) {
  // Add un-styled range before the styled span starts.
  // Process ranges calling formatter for each range.
  var result = [];
  for (var i = 0; i < spans.length; i++) {
    var span = spans[i];

    // Add un-styled range before the styled span starts.
    if (start < span.at) {
      result.push(formatter.call(context, null, undefined, line.slice(start, span.at)));
      start = span.at;
    }
    // Get all spans which are within current span.
    var subspans = [];
    for (var si = i + 1; si < spans.length && spans[si].at < span.at + span.len; si++) {
      subspans.push(spans[si]);
      i = si;
    }

    var tag = HTML_TAGS[span.tp] || {};
    result.push(formatter.call(context, span.tp, span.data,
      tag.isVoid ? null : forEach(line, start, span.at + span.len, subspans, formatter, context)));

    start = span.at + span.len;
  }

  // Add the last unformatted range.
  if (start < end) {
    result.push(formatter.call(context, null, undefined, line.slice(start, end)));
  }

  return result;
}
