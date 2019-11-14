// Package drafty contains utilities for conversion from Drafty to plain text.
package drafty

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

var errUnrecognizedContent = errors.New("content unrecognized")
var errInvalidContent = errors.New("invalid format")

type span struct {
	tp   string
	at   int
	end  int
	key  int
	data map[string]interface{}
}

type spanfmt struct {
	dec    string
	isVoid bool
}

var tags = map[string]spanfmt{
	"ST": {"*", false},
	"EM": {"_", false},
	"DL": {"~", false},
	"CO": {"", false},
	"BR": {"\n", true},
	"LN": {"", false},
	"MN": {"", false},
	"HT": {"", false},
	"IM": {"", true},
	"EX": {"", true},
}

// ToPlainText converts message payload from Drafy format to string.
// If content is plain string, then it's returned unchanged. If content is not recognized
// as either Drafy (as a map[string]interface{}) or as a string, an error is returned.
func ToPlainText(content interface{}) (string, error) {
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
		return "", errUnrecognizedContent
	}

	txt, txtOK := drafty["txt"].(string)
	fmt, fmtOK := drafty["fmt"].([]interface{})
	ent, entOK := drafty["ent"].([]interface{})

	// At least one must be set.
	if !txtOK && !fmtOK && !entOK {
		return "", errUnrecognizedContent
	}

	if fmt == nil {
		if txtOK {
			return txt, nil
		}
		return "", errUnrecognizedContent
	}

	textLen := utf8.RuneCountInString(txt)

	var spans []*span
	for i := range fmt {
		s := span{}
		f, _ := fmt[i].(map[string]interface{})
		if f == nil {
			continue
		}

		s.tp, _ = f["tp"].(string)
		tmp, _ := f["at"].(float64)
		s.at = int(tmp)
		tmp, _ = f["len"].(float64)
		s.end = s.at + int(tmp)
		if s.end > textLen || s.end < s.at {
			return "", errInvalidContent
		}
		tmp, _ = f["key"].(float64)
		s.key = int(tmp)
		// Denormalize entities into spans.
		if s.tp == "" && entOK {
			if s.key < 0 || s.key >= len(ent) {
				return "", errInvalidContent
			}

			e, _ := ent[s.key].(map[string]interface{})
			if e == nil {
				continue
			}
			s.data, _ = e["data"].(map[string]interface{})
			s.tp, _ = e["tp"].(string)
		}
		if s.tp == "" && s.at == 0 && s.end == 0 && s.key == 0 {
			return "", errUnrecognizedContent
		}
		spans = append(spans, &s)
	}

	// Sort spans first by start index (asc) then by length (desc).
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].at == spans[j].at {
			// longer one comes first
			return spans[i].end > spans[j].end
		}
		return spans[i].at < spans[j].at
	})

	return forEach([]rune(txt), 0, textLen, spans), nil
}

func forEach(line []rune, start, end int, spans []*span) string {
	// Process ranges calling formatter for each range.
	var result []string
	for i := 0; i < len(spans); i++ {
		sp := spans[i]

		if sp.at < 0 {
			// Attachment
			result = append(result, formatter(sp.tp, sp.data, ""))
			continue
		}

		// Add un-styled range before the styled span starts.
		if start < sp.at {
			result = append(result, formatter("", nil, string(line[start:sp.at])))
			start = sp.at
		}
		// Get all spans which are within current span.
		var subspans []*span
		for si := i + 1; si < len(spans) && spans[si].at < sp.end; si++ {
			subspans = append(subspans, spans[si])
			i = si
		}

		tag := tags[sp.tp]
		if tag.isVoid {
			result = append(result, formatter(sp.tp, sp.data, ""))
		} else {
			result = append(result, formatter(sp.tp, sp.data, forEach(line, start, sp.end, subspans)))
		}
		start = sp.end
	}

	// Add the last unformatted range.
	if start < end {
		result = append(result, formatter("", nil, string(line[start:end])))
	}

	return strings.Join(result, "")
}

func formatter(tp string, data map[string]interface{}, value string) string {
	switch tp {
	case "ST", "EM", "DL", "CO":
		return tags[tp].dec + value + tags[tp].dec
	case "LN":
		url := data["url"].(string)
		if url != value {
			return "[" + value + "](" + url + ")"
		}
		return value
	case "MN", "HT":
		return value
	case "BR":
		return "\n"
	case "IM":
		name, _ := data["name"].(string)
		return "[IMAGE '" + name + "']"
	case "EX":
		name, _ := data["name"].(string)
		return "[FILE '" + name + "']"
	default:
		return value
	}
}
