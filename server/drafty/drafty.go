// Package drafty contains utilities for conversion from Drafty to plain text.
package drafty

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

const (
	// Maximum size of style payload in preview, in bytes.
	maxDataSize = 128
	// Maximum count of payload fields in preview.
	maxDataCount = 8
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
	Txt string `json:"txt,omitempty"`
	txt []rune
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

type node struct {
	txt      []rune
	sp       *span
	children []*node
}

type previewState struct {
	drafty    *document
	maxLength int
	keymap    map[int]int
}

// Preview shortens Drafty to the specified length (in runes), removes quoted text, leading line breaks,
// and large content from entities making them suitable for a one-line preview,
// for example for showing in push notifications.
// The return value is a Drafty document encoded as JSON string.
func Preview(content interface{}, length int) (string, error) {
	doc, err := decodeAsDrafty(content)
	if err != nil {
		return "", err
	}
	if doc == nil {
		return "", nil
	}

	tree, err := toTree(doc)
	if err != nil {
		return "", err
	}
	if tree == nil {
		return "", nil
	}

	state := previewState{
		drafty: &document{
			Fmt: make([]style, 0, len(doc.Fmt)),
			Ent: make([]entity, 0, len(doc.Ent)),
		},
		maxLength: length,
		keymap:    make(map[int]int),
	}

	if err = previewFormatter(tree, &state); err != nil {
		return "", err
	}

	state.drafty.Txt = string(state.drafty.txt)
	data, err := json.Marshal(state.drafty)
	return string(data), err
}

type plainTextState struct {
	txt string
}

// PlainText converts drafty document to plain text with some basic markdown-like formatting.
// Deprecated. Use Preview for new development.
func PlainText(content interface{}) (string, error) {
	doc, err := decodeAsDrafty(content)
	if err != nil {
		return "", err
	}
	if doc == nil {
		return "", nil
	}

	tree, err := toTree(doc)
	if err != nil {
		return "", err
	}

	state := plainTextState{}

	err = plainTextFormatter(tree, &state)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(state.txt)), nil
}

// styleToSpan converts Drafty style to internal representation.
func (s *span) styleToSpan(in *style) error {
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

type spanfmt struct {
	dec    string
	isVoid bool
}

// Plain text formatting of the Drafty tags. Only non-blank tags need to be listed.
var tags = map[string]spanfmt{
	"BR": {"\n", true},
	"CO": {"`", false},
	"DL": {"~", false},
	"EM": {"_", false},
	"EX": {"", true},
	"ST": {"*", false},
}

// Type of the formatter to apply to tree nodes.
type formatter func(n *node, state interface{}) error

// toTree converts a drafty document into a tree of formatted spans.
// Each node of the tree is uniformly formatted.
func toTree(drafty *document) (*node, error) {
	if len(drafty.Fmt) == 0 {
		return &node{txt: drafty.txt}, nil
	}

	textLen := len(drafty.txt)

	var spans []*span
	for i := range drafty.Fmt {
		s := span{}
		if err := s.styleToSpan(&drafty.Fmt[i]); err != nil {
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

	// Drop the second format when spans overlap like '_first *second_ third*'.
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

	// Iterate over an array of spans.
	children, err := forEach(drafty.txt, 0, textLen, filtered)
	if err != nil {
		return nil, err
	}

	return &node{children: children}, nil
}

// forEach recursively iterates nested spans to form a tree.
func forEach(line []rune, start, end int, spans []*span) ([]*node, error) {
	var result []*node

	// Process ranges calling iterator for each range.
	for i := 0; i < len(spans); i++ {
		sp := spans[i]

		if sp.at < 0 {
			// Attachment
			result = append(result, &node{sp: sp})
			continue
		}

		// Add un-styled range before the styled span starts.
		if start < sp.at {
			result = append(result, &node{txt: line[start:sp.at]})
			start = sp.at
		}

		// Get all spans which are within current span.
		var subspans []*span
		for si := i + 1; si < len(spans) && spans[si].at < sp.end; si++ {
			subspans = append(subspans, spans[si])
			i = si
		}

		if tags[sp.tp].isVoid {
			result = append(result, &node{sp: sp})
		} else {
			children, err := forEach(line, start, sp.end, subspans)
			if err != nil {
				return nil, err
			}
			result = append(result, &node{children: children, sp: sp})
		}
		start = sp.end
	}

	// Add the remaining unformatted range.
	if start < end {
		result = append(result, &node{txt: line[start:end]})
	}

	return result, nil
}

// plainTextFormatter converts a tree of formatted spans into plan text.
func plainTextFormatter(n *node, ctx interface{}) error {
	if n.sp != nil && n.sp.tp == "QQ" {
		return nil
	}

	var text string
	if len(n.children) > 0 {
		state := &plainTextState{}
		for _, c := range n.children {
			if err := plainTextFormatter(c, state); err != nil {
				return err
			}
		}
		text = string(state.txt)
	} else {
		text = string(n.txt)
	}

	state := ctx.(*plainTextState)

	if n.sp == nil {
		state.txt += text
		return nil
	}

	switch n.sp.tp {
	case "ST", "EM", "DL", "CO":
		state.txt += tags[n.sp.tp].dec + text + tags[n.sp.tp].dec

	case "LN":
		if url, ok := nullableMapGet(n.sp.data, "url"); ok && url != text {
			state.txt += "[" + text + "](" + url + ")"
		} else {
			state.txt += text
		}

	case "MN", "HT":
		state.txt += text
	case "BR":
		state.txt += "\n"
	case "IM":
		name, ok := nullableMapGet(n.sp.data, "name")
		if !ok || name == "" {
			name = "?"
		}
		state.txt += "[IMAGE '" + name + "']"
	case "EX":
		name, ok := nullableMapGet(n.sp.data, "name")
		if !ok || name == "" {
			name = "?"
		}
		state.txt += "[FILE '" + name + "']"
	default:
		state.txt += text
	}
	return nil
}

// previewFormatter converts a tree of formatted spans into a shortened drafty document.
func previewFormatter(n *node, ctx interface{}) error {

	state := ctx.(*previewState)
	at := len(state.drafty.txt)
	if at >= state.maxLength {
		// Maximum doc length reached.
		return nil
	}

	if n.sp != nil {
		if n.sp.tp == "QQ" {
			// Skip quoted text
			return nil
		}
		if n.sp.tp == "BR" && at == 0 {
			// Skip leading new lines.
			return nil
		}
	}

	if len(n.children) > 0 {
		for _, c := range n.children {
			if err := previewFormatter(c, ctx); err != nil {
				return err
			}
		}
	} else {
		increment := len(n.txt)
		if at+increment > state.maxLength {
			increment = state.maxLength - at
		}
		state.drafty.txt = append(state.drafty.txt, n.txt[:increment]...)
	}

	end := len(state.drafty.txt)

	if n.sp != nil {
		fmt := style{}
		if n.sp.at < 0 {
			fmt.At = -1
		} else if at < end || tags[n.sp.tp].isVoid {
			fmt.At = at
			fmt.Length = end - at
		} else {
			return nil
		}

		if n.sp.data != nil {
			// Check if we have already seen this payload.
			key, ok := state.keymap[n.sp.key]
			if !ok {
				// Payload not found, add it.
				ent := entity{Tp: n.sp.tp, Data: copyLight(n.sp.data)}
				key = len(state.drafty.Ent)
				state.keymap[n.sp.key] = key
				state.drafty.Ent = append(state.drafty.Ent, ent)
			}
			fmt.Key = key
		} else {
			fmt.Tp = n.sp.tp
		}

		state.drafty.Fmt = append(state.drafty.Fmt, fmt)
	}
	return nil
}

// nullableMapGet is a helper method to get a possibly missing string from a possibly nil map.
func nullableMapGet(data map[string]interface{}, key string) (string, bool) {
	if data == nil {
		return "", false
	}
	str, ok := data[key].(string)
	return str, ok
}

// decodeAsDrafty converts a string or a map to a Drafty document.
func decodeAsDrafty(content interface{}) (*document, error) {
	if content == nil {
		return nil, nil
	}

	var drafty *document

	switch tmp := content.(type) {
	case string:
		drafty = &document{txt: []rune(tmp)}
	case map[string]interface{}:
		drafty = &document{}
		correct := 0
		if txt, ok := tmp["txt"].(string); ok {
			drafty.Txt = txt
			drafty.txt = []rune(txt)
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

// decodeAsStyle converts a map to a style.
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

// decodeAsEntity converts a map to a entity.
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

// A whitelist of entity fields to copy.
var lightFields = []string{"mime", "name", "width", "height", "size", "url", "ref"}

// copyLight makes a copy of an entity retaining keys from the white list.
// It also ensures the copied values are either basic types of fixed length or a
// sufficiently short string/byte slice, and the count of entries is not too great.
func copyLight(in interface{}) map[string]interface{} {
	data, ok := in.(map[string]interface{})
	if !ok {
		return nil
	}

	result := map[string]interface{}{}
	if len(data) > 0 {
		for _, key := range lightFields {
			if val, ok := data[key]; ok {
				if isFixedLengthType(val) {
					result[key] = val
				} else if l := getVariableTypeSize(val); l >= 0 && l < maxDataSize {
					result[key] = val
				}
			}

			if len(result) > maxDataCount {
				break
			}
		}
		if len(result) == 0 {
			result = nil
		}
	}
	return result
}

// intFromNumeric is a helper methjod to get an integer from a value of any numeric type.
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

// getVariableTypeSize checks that the given field is a string or a byte slice and gets its size in bytes.
func getVariableTypeSize(x interface{}) int {
	switch val := x.(type) {
	case string:
		return len(val)
	case []byte:
		return len(val)
	default:
		return -1
	}
}

// isFixedLengthType checks if the given value is a type of a fixed size.
func isFixedLengthType(x interface{}) bool {
	switch x.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16,
		uint32, uint64, float32, float64, complex64, complex128:
		return true
	default:
		return false
	}
}
