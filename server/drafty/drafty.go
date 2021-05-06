// Package drafty contains utilities for conversion from Drafty to plain text.
package drafty

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	errUnrecognizedContent = errors.New("content unrecognized")
	errInvalidContent      = errors.New("invalid format")
)

type span struct {
	tp   string
	at   int
	end  int
	key  int
	data map[string]interface{}
}

func (s *span) fromMap(in interface{}) error {
	m, _ := in.(map[string]interface{})
	if m == nil {
		return errUnrecognizedContent
	}

	s.tp, _ = m["tp"].(string)
	var err error

	s.at, err = intFromNumeric(m["at"])
	if err != nil {
		return err
	}

	s.end, err = intFromNumeric(m["len"])
	if err != nil {
		return err
	}
	if s.end < 0 {
		return errInvalidContent
	}
	s.end += s.at

	if s.tp == "" {
		s.key, err = intFromNumeric(m["key"])
		if err != nil {
			return err
		}
		if s.key < 0 {
			return errInvalidContent
		}
	}

	return nil
}

func intFromNumeric(num interface{}) (int, error) {
	if num == nil {
		return 0, nil
	}
	switch i := num.(type) {
	case int:
		return i, nil
	case int16:
		return int(i), nil
	case int32:
		return int(i), nil
	case int64:
		return int(i), nil
	case float32:
		return int(i), nil
	case float64:
		return int(i), nil
	default:
		return 0, errInvalidContent
	}
}

func (s *span) toMap() map[string]interface{} {
	out := make(map[string]interface{})

	if s.tp != "" {
		out["tp"] = s.tp
	} else {
		out["key"] = s.key
	}
	if s.at != 0 {
		out["at"] = s.at
	}
	if s.end != s.at {
		out["len"] = s.end - s.at
	}

	return out
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

// Preview shortens Drafty to the specified length (in runes) and removes large content from entities making them
// suitable for a one-line preview, for example for showing in push notifications.
func Preview(content interface{}, length int) (string, error) {
	if content == nil {
		return "", nil
	}

	var original map[string]interface{}

	switch tmp := content.(type) {
	case string:
		return tmp, nil
	case map[string]interface{}:
		original = tmp
	default:
		return "", errUnrecognizedContent
	}

	txt, txtOK := original["txt"].(string)
	fmt, fmtOK := original["fmt"].([]interface{})
	ent, entOK := original["ent"].([]interface{})

	// At least one must be set.
	if !txtOK && !fmtOK && !entOK {
		return "", errUnrecognizedContent
	}

	var textLen int
	preview := make(map[string]interface{})
	if txtOK {
		runes := []rune(txt)
		textLen = len(runes)
		if textLen > length {
			txt = string(runes[:length])
			textLen = length
		}

		preview["txt"] = txt
	}

	if len(fmt) > 0 {
		// Old key to new key entity mapping.
		entRefs := make(map[int]int)

		// Cache styles which start within the new length of the text and save entity keys as set.
		var styles []span
		for i := range fmt {
			s := span{}
			if err := s.fromMap(fmt[i]); err != nil {
				return "", err
			}

			if s.at < textLen {
				styles = append(styles, s)
				if s.tp == "" {
					if s.key < 0 {
						return "", errUnrecognizedContent
					}

					if _, ok := entRefs[s.key]; !ok {
						entRefs[s.key] = len(entRefs)
					}
				}
			}
		}

		// Allocate space for copying styles and entities.
		var previewFmt []map[string]interface{}
		var previewEnt []map[string]interface{}
		if len(entRefs) > 0 {
			previewEnt = make([]map[string]interface{}, len(entRefs))
		}
		for _, old := range styles {
			style := span{at: old.at, end: old.end}
			if old.tp != "" {
				style.tp = old.tp
			} else if old.key >= 0 && len(ent) > old.key {
				if key, ok := entRefs[old.key]; ok {
					style.key = key
					previewEnt[style.key] = copyLight(ent[old.key])
				} else {
					continue
				}
			} else {
				continue
			}
			previewFmt = append(previewFmt, style.toMap())
		}

		if len(previewFmt) > 0 {
			preview["fmt"] = previewFmt
		}
		if len(previewEnt) > 0 {
			preview["ent"] = previewEnt
		}
	}

	data, err := json.Marshal(preview)

	return string(data), err
}

var lightData = []string{"mime", "name", "width", "height", "size"}

func copyLight(in interface{}) map[string]interface{} {
	ent, ok := in.(map[string]interface{})
	if !ok {
		return nil
	}

	tp, _ := ent["tp"].(string)
	data, _ := ent["data"].(map[string]interface{})
	result := map[string]interface{}{"tp": tp}
	var dc map[string]interface{}
	if len(data) > 0 {
		dc = make(map[string]interface{})
		for _, key := range lightData {
			if val, ok := data[key]; ok {
				dc[key] = val
			}
		}
		if len(dc) != 0 {
			result["data"] = dc
		}
	}
	return result
}

// ToPlainText converts message payload from Drafy to string.
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
		if err := s.fromMap(fmt[i]); err != nil {
			return "", err
		}
		if s.end > textLen {
			return "", errInvalidContent
		}

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
		url, _ := data["url"].(string)
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
