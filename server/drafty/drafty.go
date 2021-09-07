// Package drafty contains utilities for conversion from Drafty to plain text.
package drafty

import (
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
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

func (n *node) String() string {
	str := "{"
	if n == nil {
		str = "nil"
	} else {
		if n.sp != nil {
			str = n.sp.tp + ":"
		}

		if n.txt != nil {
			str += "'" + string(n.txt) + "'"
		} else if len(n.children) > 0 {
			str += "["
			var sub []string
			for _, c := range n.children {
				sub = append(sub, c.String())
			}
			str += strings.Join(sub, ",") + "]"
		} else if n.sp != nil && n.sp.at < 0 {
			// An attachment.
			str += "(att)"
		}
	}
	return str + "}"
}

type previewState struct {
	length int
	keymap map[int]int
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

	state := previewState{
		length: length,
	}
	tree, err := iterate(doc, previewFormatter, &state)
	if err != nil {
		return "", err
	}
	if tree == nil {
		return "", nil
	}

	log.Println("T:", tree.(*node).String())

	preview := &document{
		// txt: txt,
		Fmt: make([]style, 0, len(doc.Fmt)),
		Ent: make([]entity, 0, len(doc.Ent)),
	}
	treeToDrafty(tree.(*node), preview)
	preview.Txt = string(preview.txt)
	data, err := json.Marshal(preview)
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

	if text, ok := result.(string); ok {
		return text, nil
	}
	return "", errInvalidContent
}

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

var tags = map[string]spanfmt{
	"ST": {"*", false},
	"EM": {"_", false},
	"DL": {"~", false},
	"CO": {"`", false},
	"BR": {"\n", true},
	"LN": {"", false},
	"MN": {"", false},
	"HT": {"", false},
	"IM": {"", false},
	"EX": {"", true},
	"QQ": {"", false},
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
		return handler(drafty.txt, nil, state)
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
	tree, err := forEach(drafty.txt, 0, textLen, filtered, handler, state)
	if err != nil {
		return nil, err
	}

	return handler(tree, nil, state)
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
			if content, err = handler(nil, sp, state); err != nil {
				return nil, err
			}
			if content != nil {
				result = append(result, content)
			}
			continue
		}

		// Add un-styled range before the styled span starts.
		if start < sp.at {
			if content, err = handler(line[start:sp.at], nil, state); err != nil {
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
			content, err = handler(nil, sp, state)
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
		if content, err = handler(line[start:end], nil, state); err != nil {
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
	if value == nil {
		value = ""
	}
	switch content := value.(type) {
	case string:
		if s == nil {
			return content, nil
		}

		switch s.tp {
		case "ST", "EM", "DL", "CO":
			return tags[s.tp].dec + content + tags[s.tp].dec, nil
		case "LN":
			if url, ok := nullableMapGet(s.data, "url"); ok && url != content {
				return "[" + content + "](" + url + ")", nil
			}
			return content, nil
		case "MN", "HT":
			return content, nil
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
			return content, nil
		}
	case []rune:
		return plainTextFormatter(string(content), s, nil)
	case []iteratorContent:
		var text string
		for _, block := range content {
			text += block.(string)
		}
		return plainTextFormatter(text, s, nil)
	default:
		return nil, errUnrecognizedContent
	}
}

// Preview formatter converts
func previewFormatter(value interface{}, s *span, state interface{}) (iteratorContent, error) {
	if s != nil && s.tp == "QQ2" {
		// Skip quoted text
		return nil, nil
	}

	ctx := state.(*previewState)

	switch content := value.(type) {
	case nil:
		if s == nil || s.end > s.at {
			// Blank or dangling element.
			return nil, nil
		}
		return &node{sp: s}, nil
	case []rune:
		ln := len(content)
		if ctx.length <= 0 && ln > 0 {
			return nil, nil
		}
		if ln > ctx.length {
			content = content[:ctx.length]
			ln = ctx.length
		}
		ctx.length -= ln
		return &node{txt: content, sp: s}, nil
	case []iteratorContent:
		var children []*node
		for _, block := range content {
			children = append(children, block.(*node))
		}
		return &node{sp: s, children: children}, nil
	default:
		log.Printf("unknown %#+v", content)
		return nil, errUnrecognizedContent
	}
}

// Reassemble drafty document from a tree of style spans.
func treeToDrafty(n *node, doc *document) {
	at := len(doc.txt)
	if len(n.children) > 0 {
		for _, child := range n.children {
			treeToDrafty(child, doc)
		}
	} else {
		doc.txt = append(doc.txt, n.txt...)
	}
	end := len(doc.txt)

	if n.sp != nil {
		fmt := style{}
		if n.sp.at < 0 {
			fmt.At = -1
		} else if at < end || tags[n.sp.tp].isVoid {
			fmt.At = at
			fmt.Length = end - at
		} else {
			return
		}

		if n.sp.data != nil {
			ent := entity{Tp: n.sp.tp, Data: copyLight(n.sp.data)}
			fmt.Key = len(doc.Ent)
			doc.Ent = append(doc.Ent, ent)
		} else {
			fmt.Tp = n.sp.tp
		}

		doc.Fmt = append(doc.Fmt, fmt)
	}
}

/*
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
*/

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

func isFixedLengthType(x interface{}) bool {
	switch x.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16,
		uint32, uint64, float32, float64, complex64, complex128:
		return true
	default:
		return false
	}
}
