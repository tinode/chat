// Package drafty contains utilities for conversion from Drafty to plain text.
package drafty

import (
	"encoding/json"
	"errors"
	"sort"
	"unicode/utf8"
)

const (
	// Maximum size of style payload in preview, in bytes.
	maxDataSize = 128
	// Maximum count of payload fields in preview.
	maxDataCount = 16
)

var (
	errUnrecognizedContent = errors.New("content unrecognized")
	errInvalidContent      = errors.New("invalid format")
)

type style struct {
	Tp     string `json:"tp,omitempty"`
	At     int    `json:"at,omitempty"`
	Length int    `json:"len,omitempty"`
	Key    int    `json:"key,omitempty"`
}

type entity struct {
	Tp   string                 `json:"tp,omitempty"`
	Data map[string]interface{} `json:"data,omitempty"`
}

type document struct {
	Txt string   `json:"txt,omitempty"`
	Fmt []style  `json:"fmt,omitempty"`
	Ent []entity `json:"ent,omitempty"`
}

type span struct {
	tp   string
	at   int
	end  int
	key  int
	data map[string]interface{}
}

type previewState struct {
	// New length of the document.
	length int
	// Mapping of old entity keys to new to prevent entity duplication.
	keymap map[int]int
	// The document itself.
	preview *document
}

// Preview shortens Drafty to the specified length (in runes), removes quoted text and large content from entities
// making them suitable for a one-line preview, for example for showing in push notifications.
// The return value is a Drafty document encoded as JSON string.
func Preview(content interface{}, length int) (string, error) {
	doc, err := decodeAsDrafty(content)
	if err != nil {
		return "", err
	}
	if doc == nil {
		return "", nil
	}
	txt := []rune(doc.Txt)
	if len(txt) > length {
		txt = txt[:length]
	}
	state := previewState{
		length: length,
		keymap: make(map[int]int),
		preview: &document{
			Txt: string(txt),
			Fmt: make([]style, 0, len(doc.Fmt)),
			Ent: make([]entity, 0, len(doc.Ent)),
		},
	}

	iterate(doc, previewFormatter, &state)

	data, err := json.Marshal(state.preview)
	return string(data), err
}

func ToPlainText(content interface{}) (string, error) {
	doc, err := decodeAsDrafty(content)
	if err != nil {
		return "", err
	}
	if doc == nil {
		return "", nil
	}

	result, err := iterate(doc, plainTextFormatter, nil)
	if err != nil {
		return "", err
	}
	text, ok := result.(string)
	if !ok {
		return "", errInvalidContent
	}
	return text, nil
}

func (s *span) fromStyle(in *style) error {
	s.tp = in.Tp
	s.at = in.At
	s.end = in.Length
	if s.end < 0 {
		return errInvalidContent
	}
	s.end += s.at

	if s.tp == "" {
		s.key = in.Key
		if s.key < 0 {
			return errInvalidContent
		}
	}

	return nil
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

var lightFields = []string{"mime", "name", "width", "height", "size", "url", "ref"}

func copyLight(in interface{}) map[string]interface{} {
	data, ok := in.(map[string]interface{})
	if !ok {
		return nil
	}

	result := map[string]interface{}{}
	if len(data) > 0 {
		for _, key := range lightFields {
			if val, ok := data[key]; ok {
				result[key] = val
			}
		}
		if len(result) == 0 {
			result = nil
		}
	}
	return result
}

type iteratorContent interface{}
type iterationHandler func(content interface{}, sp *span, state interface{}) (iteratorContent, error)

// Call handler for each styled or unstyled text span.
// - content: a drafty document to process.
// - handler: function to call for each styled or unstyled span.
// - context: handler's context.
func iterate(drafty *document, handler iterationHandler, state interface{}) (iteratorContent, error) {
	if len(drafty.Fmt) == 0 {
		return handler(drafty.Txt, nil, state)
	}

	textLen := utf8.RuneCountInString(drafty.Txt)

	var spans []*span
	for i := range drafty.Fmt {
		s := span{}
		if err := s.fromStyle(&drafty.Fmt[i]); err != nil {
			return nil, err
		}
		if s.at < -1 || s.end > textLen {
			return nil, errInvalidContent
		}

		// Denormalize entities into spans.
		if s.tp == "" && len(drafty.Ent) > 0 {
			if s.key < 0 || s.key >= len(drafty.Ent) {
				return nil, errInvalidContent
			}

			s.data = drafty.Ent[s.key].Data
			s.tp = drafty.Ent[s.key].Tp
		}
		if s.tp == "" && s.at == 0 && s.end == 0 && s.key == 0 {
			return nil, errUnrecognizedContent
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

	// Drop overlapping spans like '_first *second_ third*'.
	var filtered []*span
	end := -2
	for _, span := range spans {
		if span.at < end && span.end > end {
			continue
		}
		filtered = append(filtered, span)
		if span.end > end {
			end = span.end
		}
	}

	// Iterate over spans.
	children, err := forEach([]rune(drafty.Txt), 0, textLen, filtered, handler, state)
	if err != nil {
		return nil, err
	}
	return handler(children, nil, state)
}

func forEach(line []rune, start, end int, spans []*span, handler iterationHandler, state interface{}) ([]iteratorContent, error) {
	var result []iteratorContent
	var content iteratorContent

	var err error
	// Process ranges calling iterator for each range.
	for i := 0; i < len(spans); i++ {
		sp := spans[i]

		if sp.at < 0 {
			// Attachment
			if content, err = handler("", sp, state); err != nil {
				return nil, err
			}
			if content != nil {
				result = append(result, content)
			}
			continue
		}

		// Add un-styled range before the styled span starts.
		if start < sp.at {
			if content, err = handler(string(line[start:sp.at]), nil, state); err != nil {
				return nil, err
			}
			if content != nil {
				result = append(result, content)
			}
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
			content, err = handler("", sp, state)
		} else {
			content, err = forEach(line, start, sp.end, subspans, handler, state)
			if err != nil {
				return nil, err
			}
			content, err = handler(content, sp, state)
		}
		if err != nil {
			return nil, err
		}
		if content != nil {
			result = append(result, content)
		}
		start = sp.end
	}

	// Add the remaining unformatted range.
	if start < end {
		if content, err = handler(string(line[start:end]), nil, state); err != nil {
			return nil, err
		}

		if content != nil {
			result = append(result, content)
		}
	}

	return result, nil
}

// Convert a uniform style to plan text.
func plainTextFormatter(value interface{}, s *span, _ interface{}) (iteratorContent, error) {
	switch text := value.(type) {
	case string:
		if s == nil {
			return text, nil
		}

		switch s.tp {
		case "ST", "EM", "DL", "CO":
			return tags[s.tp].dec + text + tags[s.tp].dec, nil
		case "LN":
			if url, ok := nullableMapGet(s.data, "url"); ok && url != text {
				return "[" + text + "](" + url + ")", nil
			}
			return value, nil
		case "MN", "HT":
			return text, nil
		case "BR":
			return "\n", nil
		case "IM":
			name, ok := nullableMapGet(s.data, "name")
			if !ok || name == "" {
				name = "?"
			}
			return "[IMAGE '" + name + "']", nil
		case "EX":
			name, ok := nullableMapGet(s.data, "name")
			if !ok || name == "" {
				name = "?"
			}
			return "[FILE '" + name + "']", nil
		default:
			return text, nil
		}

	case []iteratorContent:
		var concat string
		for _, block := range text {
			concat += block.(string)
		}
		return plainTextFormatter(concat, s, nil)
	default:
		return nil, errUnrecognizedContent
	}
}

// Preview formatter converts
func previewFormatter(value interface{}, s *span, state interface{}) (iteratorContent, error) {
	if s == nil {
		return nil, nil
	}

	context := state.(*previewState)
	if s.at >= context.length {
		return nil, nil
	}

	st := style{
		At: s.at,
	}
	st.Length = s.end - s.at
	if st.At+st.Length > context.length {
		st.Length = context.length - st.At
	}

	if s.data != nil {
		// The span has an entity.
		if key, ok := context.keymap[s.key]; !ok {
			// New entity.
			st.Key = len(context.preview.Ent)
			context.keymap[s.key] = st.Key
			context.preview.Ent = append(context.preview.Ent, entity{Tp: s.tp, Data: copyLight(s.data)})
		} else {
			// Entity already added.
			st.Key = key
		}
	} else {
		st.Tp = s.tp
	}
	context.preview.Fmt = append(context.preview.Fmt, st)
	return nil, nil
}

func nullableMapGet(data map[string]interface{}, key string) (string, bool) {
	if data == nil {
		return "", false
	}
	str, ok := data[key].(string)
	return str, ok
}

func decodeAsDrafty(content interface{}) (*document, error) {
	if content == nil {
		return nil, nil
	}

	var drafty *document

	switch tmp := content.(type) {
	case string:
		drafty = &document{Txt: tmp}
	case map[string]interface{}:
		drafty = &document{}
		correct := 0
		if txt, ok := tmp["txt"].(string); ok {
			drafty.Txt = txt
			correct++
		}
		if ifmt, ok := tmp["fmt"].([]interface{}); ok {
			for i := range ifmt {
				st, err := decodeAsStyle(ifmt[i])
				if err != nil {
					return nil, err
				}
				if st != nil {
					drafty.Fmt = append(drafty.Fmt, *st)
				}
				correct++
			}
		}
		if ient, ok := tmp["ent"].([]interface{}); ok {
			for i := range ient {
				ent, err := decodeAsEntity(ient[i])
				if err != nil {
					return nil, err
				}
				if ent != nil {
					drafty.Ent = append(drafty.Ent, *ent)
				}
				correct++
			}
		}
		// At least one drafty element must be present.
		if correct == 0 {
			return nil, errUnrecognizedContent
		}
	default:
		return nil, errUnrecognizedContent
	}

	return drafty, nil
}

func decodeAsStyle(content interface{}) (*style, error) {
	if content == nil {
		return nil, nil
	}

	tmp, ok := content.(map[string]interface{})
	if !ok {
		return nil, errUnrecognizedContent
	}

	var err error
	st := &style{}
	st.Tp, _ = tmp["tp"].(string)

	st.At, err = intFromNumeric(tmp["at"])
	if err != nil {
		return nil, err
	}

	st.Length, err = intFromNumeric(tmp["len"])
	if err != nil {
		return nil, err
	}

	if st.Tp == "" {
		st.Key, err = intFromNumeric(tmp["key"])
		if err != nil {
			return nil, err
		}
		if st.Key < 0 {
			return nil, errInvalidContent
		}
	}

	return st, nil
}

func decodeAsEntity(content interface{}) (*entity, error) {
	if content == nil {
		return nil, nil
	}

	tmp, ok := content.(map[string]interface{})
	if !ok {
		return nil, errUnrecognizedContent
	}

	ent := &entity{}

	ent.Tp, _ = tmp["tp"].(string)
	if ent.Tp == "" {
		return nil, errInvalidContent
	}

	ent.Data, _ = tmp["data"].(map[string]interface{})

	return ent, nil
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

// Check that the given field is indeed integer.
func getIntSize(data map[string]interface{}, key string) int {
	val, ok := data[key]
	if ok {
		_, err := intFromNumeric(val)
		if err == nil {
			return 4
		}
	}
	return -1
}

// Check that the given field is indeed string and get its size in bytes.
func getStringSize(data map[string]interface{}, key string) int {
	val, ok := data[key].(string)
	if ok {
		return len(val)
	}
	return -1
}

// Check that the given field is indeed a byte slice and get its size.
func getByteSliceSize(data map[string]interface{}, key string) int {
	val, ok := data[key].([]byte)
	if ok {
		return len(val)
	}
	return -1
}
