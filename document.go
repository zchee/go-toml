// Copyright 2026 The go-toml Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package toml

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Document is a format-preserving TOML document.
//
// It stores the original raw bytes and records byte spans for parsed key/value
// entries. Untouched documents serialize byte-for-byte identically to the input;
// mutations rewrite only the affected value, line, or insertion point.
type Document struct {
	raw []byte

	entries []documentEntry
	byPath  map[string]int

	replacements map[string][]byte
	deletions    map[string]struct{}
	insertions   []documentInsertion
	pending      map[string]any
}

type documentEntry struct {
	pathKey   string
	value     any
	valueSet  bool
	valueRaw  []byte
	valueSpan [2]int
	lineSpan  [2]int
	kind      documentValueKind
}

type documentValueKind uint8

const (
	documentKindInvalid documentValueKind = iota
	documentKindString
	documentKindInteger
	documentKindFloat
	documentKindBool
	documentKindDatetime
	documentKindArray
	documentKindInlineTable
)

type documentInsertion struct {
	afterPath string
	pathKey   string
	path      []string
	value     any
}

type documentOp struct {
	start int
	end   int
	text  []byte
	order int
}

type spannedToken struct {
	Token
	span [2]int
}

type documentParser struct {
	data     []byte
	dec      *Decoder
	interner documentPathInterner
}

type documentPathPair struct {
	parent string
	child  string
}

type documentPathInterner struct {
	segments map[string]string
	keys     map[string]string
	joins    map[documentPathPair]string
}

// ParseDocument parses data into a format-preserving Document.
func ParseDocument(data []byte) (*Document, error) {
	raw := append([]byte(nil), data...)
	entryHint := documentEntryHint(raw)
	p := &documentParser{data: raw, dec: NewDecoderBytes(raw, withoutTokenPositions)}
	doc := &Document{
		raw:     raw,
		entries: make([]documentEntry, 0, entryHint),
		byPath:  make(map[string]int, documentMapHint),
	}
	currentPathKey := ""
	for {
		tok, err := p.readToken()
		if errors.Is(err, io.EOF) {
			return doc, nil
		}
		if err != nil {
			return nil, err
		}
		switch tok.Kind {
		case TokenKindComment:
			continue
		case TokenKindTableHeader:
			pathKey, err := p.parseHeaderPathKey(tok.Bytes, false)
			if err != nil {
				return nil, err
			}
			currentPathKey = pathKey
		case TokenKindArrayTableHeader:
			pathKey, err := p.parseHeaderPathKey(tok.Bytes, true)
			if err != nil {
				return nil, err
			}
			currentPathKey = pathKey
		case TokenKindKey:
			keyPathKey, err := p.parseDottedPathKey(tok.Bytes)
			if err != nil {
				return nil, err
			}
			valueRaw, valueSpan, kind, err := p.readValue()
			if err != nil {
				return nil, err
			}
			pathKey := p.interner.joinPathKeys(currentPathKey, keyPathKey)
			entry := documentEntry{
				pathKey:   pathKey,
				valueRaw:  valueRaw,
				valueSpan: valueSpan,
				lineSpan:  lineSpan(raw, tok.span[0], valueSpan[1]),
				kind:      kind,
			}
			doc.byPath[entry.pathKey] = len(doc.entries)
			doc.entries = append(doc.entries, entry)
		default:
			if currentPathKey == "" {
				return nil, syntaxErrorAtOffset(data, tok.Offset, "unexpected token", tok.span)
			}
		}
	}
}

// Raw returns the document's original byte arena.
//
// Callers must treat the returned slice as read-only. Mutating it changes the
// bytes that future format-preserving operations observe.
func (d *Document) Raw() []byte {
	if d == nil {
		return nil
	}
	return d.raw
}

// Get returns the parsed value at path. Path accepts TOML dotted-key syntax,
// including quoted segments.
func (d *Document) Get(path string) (any, bool) {
	if d == nil {
		return nil, false
	}
	parts, err := parseDocumentPath(path)
	if err != nil {
		return nil, false
	}
	entry, ok := d.entryForPathKey(joinDocumentPath(parts))
	if !ok {
		value, ok := d.pending[joinDocumentPath(parts)]
		if ok {
			return value, true
		}
		return nil, false
	}
	if _, deleted := d.deletions[entry.pathKey]; deleted {
		return nil, false
	}
	if !entry.valueSet {
		value, err := parseRawDocumentValue(entry.valueRaw)
		if err != nil {
			return nil, false
		}
		entry.value = value
		entry.valueSet = true
	}
	return entry.value, true
}

// Set replaces path with value. Existing values rewrite only their value span;
// new values are appended as canonical TOML key/value lines.
func (d *Document) Set(path string, value any) error {
	if d == nil {
		return errors.New("toml: nil Document")
	}
	parts, err := parseDocumentPath(path)
	if err != nil {
		return err
	}
	pathKey := joinDocumentPath(parts)
	repl, err := d.formatValueForPath(pathKey, value)
	if err != nil {
		return err
	}
	if entry, ok := d.entryForPathKey(pathKey); ok {
		entry.value = value
		entry.valueSet = true
		if d.replacements == nil {
			d.replacements = make(map[string][]byte, 1)
		}
		d.replacements[pathKey] = repl
		delete(d.deletions, pathKey)
		delete(d.pending, pathKey)
		return nil
	}
	if d.pending == nil {
		d.pending = make(map[string]any, 1)
	}
	d.pending[pathKey] = value
	d.insertions = append(d.insertions, documentInsertion{pathKey: pathKey, path: parts, value: value})
	return nil
}

// InsertAfter inserts path after afterPath using canonical TOML key/value
// syntax. afterPath must identify an existing, non-deleted entry.
func (d *Document) InsertAfter(afterPath, path string, value any) error {
	if d == nil {
		return errors.New("toml: nil Document")
	}
	afterParts, err := parseDocumentPath(afterPath)
	if err != nil {
		return err
	}
	afterKey := joinDocumentPath(afterParts)
	if _, ok := d.byPath[afterKey]; !ok {
		return fmt.Errorf("toml: insert anchor %q not found", afterPath)
	}
	if _, deleted := d.deletions[afterKey]; deleted {
		return fmt.Errorf("toml: insert anchor %q deleted", afterPath)
	}
	parts, err := parseDocumentPath(path)
	if err != nil {
		return err
	}
	pathKey := joinDocumentPath(parts)
	if d.pending == nil {
		d.pending = make(map[string]any, 1)
	}
	d.pending[pathKey] = value
	d.insertions = append(d.insertions, documentInsertion{afterPath: afterKey, pathKey: pathKey, path: parts, value: value})
	return nil
}

// Delete removes the key/value line at path.
func (d *Document) Delete(path string) error {
	if d == nil {
		return errors.New("toml: nil Document")
	}
	parts, err := parseDocumentPath(path)
	if err != nil {
		return err
	}
	pathKey := joinDocumentPath(parts)
	if _, ok := d.byPath[pathKey]; !ok {
		if _, ok := d.pending[pathKey]; ok {
			delete(d.pending, pathKey)
			return nil
		}
		return fmt.Errorf("toml: path %q not found", path)
	}
	if d.deletions == nil {
		d.deletions = make(map[string]struct{}, 1)
	}
	d.deletions[pathKey] = struct{}{}
	delete(d.replacements, pathKey)
	return nil
}

// Bytes serializes the document, preserving untouched bytes exactly.
func (d *Document) Bytes() []byte { //nolint:cyclop // document edit-op assembly over replace/delete/insert spans; cohesive.
	if d == nil {
		return nil
	}
	if len(d.replacements) == 0 && len(d.deletions) == 0 && len(d.insertions) == 0 {
		return d.raw
	}
	ops := make([]documentOp, 0, len(d.replacements)+len(d.deletions)+len(d.insertions))
	order := 0
	for i := range d.entries {
		entry := &d.entries[i]
		if _, ok := d.deletions[entry.pathKey]; ok {
			ops = append(ops, documentOp{start: entry.lineSpan[0], end: entry.lineSpan[1], order: order})
			order++
			continue
		}
		if repl, ok := d.replacements[entry.pathKey]; ok {
			ops = append(ops, documentOp{start: entry.valueSpan[0], end: entry.valueSpan[1], text: repl, order: order})
			order++
		}
	}
	for _, ins := range d.insertions {
		value, ok := d.pending[ins.pathKey]
		if !ok {
			continue
		}
		text, err := formatDocumentKeyValue(ins.path, value)
		if err != nil {
			continue
		}
		if ins.afterPath == "" {
			prefix := []byte(nil)
			if len(d.raw) > 0 && !bytes.HasSuffix(d.raw, []byte("\n")) {
				prefix = []byte("\n")
			}
			ops = append(ops, documentOp{start: len(d.raw), end: len(d.raw), text: append(prefix, text...), order: order})
			order++
			continue
		}
		entry, ok := d.entryForPathKey(ins.afterPath)
		if !ok {
			continue
		}
		pos := entry.lineSpan[1]
		ops = append(ops, documentOp{start: pos, end: pos, text: text, order: order})
		order++
	}
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].start == ops[j].start {
			if ops[i].end == ops[j].end {
				return ops[i].order < ops[j].order
			}
			return ops[i].end < ops[j].end
		}
		return ops[i].start < ops[j].start
	})
	out := make([]byte, 0, len(d.raw)+documentOpsDelta(ops))
	cursor := 0
	for _, op := range ops {
		if op.start < cursor || op.start < 0 || op.end < op.start || op.end > len(d.raw) {
			continue
		}
		out = append(out, d.raw[cursor:op.start]...)
		out = append(out, op.text...)
		cursor = op.end
	}
	out = append(out, d.raw[cursor:]...)
	return out
}

func (p *documentParser) readToken() (spannedToken, error) {
	tok, err := p.dec.ReadToken()
	if err != nil {
		return spannedToken{}, err
	}
	return spannedToken{Token: tok, span: tokenSpan(tok)}, nil
}

func (p *documentParser) readValue() (raw []byte, span [2]int, kind documentValueKind, err error) {
	for {
		tok, err := p.readToken()
		if err != nil {
			return nil, [2]int{}, documentKindInvalid, err
		}
		if tok.Kind == TokenKindComment {
			continue
		}
		start := tok.span[0]
		end := tok.span[1]
		switch tok.Kind {
		case TokenKindArrayStart, TokenKindInlineTableStart:
			wantArray := tok.Kind == TokenKindArrayStart
			arrayDepth, inlineDepth := 0, 0
			if wantArray {
				arrayDepth = 1
			} else {
				inlineDepth = 1
			}
			for arrayDepth > 0 || inlineDepth > 0 {
				next, err := p.readToken()
				if err != nil {
					return nil, [2]int{}, documentKindInvalid, err
				}
				end = next.span[1]
				switch next.Kind {
				case TokenKindArrayStart:
					arrayDepth++
				case TokenKindArrayEnd:
					arrayDepth--
				case TokenKindInlineTableStart:
					inlineDepth++
				case TokenKindInlineTableEnd:
					inlineDepth--
				}
			}
			raw := p.data[start:end]
			kind := documentKindArray
			if !wantArray {
				kind = documentKindInlineTable
			}
			return raw, [2]int{start, end}, kind, nil
		default:
			raw := p.data[start:end]
			return raw, [2]int{start, end}, documentKindFromToken(tok.Kind), nil
		}
	}
}

func (p *documentParser) parseHeaderPathKey(raw []byte, array bool) (string, error) {
	return p.parseDottedPathKey(trimHeaderKey(raw, array))
}

//nolint:cyclop,gocognit // dotted-path key parser with quoted/bare segments; cohesive.
func (p *documentParser) parseDottedPathKey(raw []byte) (string, error) {
	origLen := len(raw)
	raw = bytesTrimSpace(raw)
	if isSimpleBareKey(raw) {
		return p.interner.internPathKey(p.interner.internSegment(raw)), nil
	}
	var b strings.Builder
	for len(raw) > 0 {
		raw = bytes.TrimLeft(raw, " \t")
		if len(raw) == 0 {
			break
		}
		var part string
		if raw[0] == '\'' || raw[0] == '"' { //nolint:nestif // quoted vs bare key-segment branch; cohesive.
			q := raw[0]
			end := 1
			for end < len(raw) {
				if raw[end] == '\\' && q == '"' {
					end += 2
					continue
				}
				if raw[end] == q {
					break
				}
				end++
			}
			if end >= len(raw) {
				return "", &SyntaxError{Line: 1, Col: 1, Msg: "unterminated quoted key", Span: [2]int{0, origLen}}
			}
			v, err := parseStringValue(raw[:end+1])
			if err != nil {
				return "", err
			}
			part = p.interner.internSegmentString(v)
			raw = raw[end+1:]
		} else {
			end := bytes.IndexAny(raw, ". \t")
			var partRaw []byte
			if end < 0 {
				partRaw = raw
				raw = raw[:0]
			} else {
				partRaw = raw[:end]
				raw = raw[end:]
			}
			if len(partRaw) == 0 {
				return "", &SyntaxError{Line: 1, Col: 1, Msg: "empty key segment", Span: [2]int{0, origLen}}
			}
			if !isSimpleBareKey(partRaw) {
				return "", &SyntaxError{Line: 1, Col: 1, Msg: "unexpected token in dotted key", Span: [2]int{0, origLen}}
			}
			part = p.interner.internSegment(partRaw)
		}
		if b.Len() > 0 {
			b.WriteByte('\x00')
		}
		b.WriteString(part)
		raw = bytes.TrimLeft(raw, " \t")
		if len(raw) == 0 {
			break
		}
		if raw[0] != '.' {
			return "", &SyntaxError{Line: 1, Col: 1, Msg: "unexpected token in dotted key", Span: [2]int{0, origLen}}
		}
		raw = raw[1:]
		if len(bytes.TrimLeft(raw, " \t")) == 0 {
			return "", &SyntaxError{Line: 1, Col: 1, Msg: "empty key segment", Span: [2]int{0, origLen}}
		}
	}
	if b.Len() == 0 {
		return "", &SyntaxError{Line: 1, Col: 1, Msg: "empty key", Span: [2]int{0, origLen}}
	}
	return p.interner.internPathKey(b.String()), nil
}

func (i *documentPathInterner) internSegment(raw []byte) string {
	if i.segments == nil {
		i.segments = make(map[string]string, documentMapHint)
	}
	if s, ok := i.segments[string(raw)]; ok {
		return s
	}
	s := string(raw)
	i.segments[s] = s
	return s
}

func (i *documentPathInterner) internSegmentString(s string) string {
	if i.segments == nil {
		i.segments = make(map[string]string, documentMapHint)
	}
	if existing, ok := i.segments[s]; ok {
		return existing
	}
	i.segments[s] = s
	return s
}

func (i *documentPathInterner) internPathKey(s string) string {
	if i.keys == nil {
		i.keys = make(map[string]string, documentMapHint)
	}
	if existing, ok := i.keys[s]; ok {
		return existing
	}
	i.keys[s] = s
	return s
}

func (i *documentPathInterner) joinPathKeys(parent, child string) string {
	switch {
	case parent == "":
		return i.internPathKey(child)
	case child == "":
		return i.internPathKey(parent)
	}
	pair := documentPathPair{parent: parent, child: child}
	if i.joins != nil {
		if key, ok := i.joins[pair]; ok {
			return key
		}
	} else {
		i.joins = make(map[documentPathPair]string, documentMapHint)
	}
	key := i.internPathKey(parent + "\x00" + child)
	i.joins[pair] = key
	return key
}

func parseRawDocumentValue(raw []byte) (any, error) {
	buf := make([]byte, 0, len(raw)+4)
	buf = append(buf, 'x', ' ', '=', ' ')
	buf = append(buf, raw...)
	dec := NewDecoderBytes(buf, withoutTokenPositions)
	if tok, err := dec.ReadToken(); err != nil || tok.Kind != TokenKindKey {
		if err != nil {
			return nil, err
		}
		return nil, syntaxErrorAtOffset(buf, tok.Offset, "expected key", [2]int{0, len(buf)})
	}
	return parseNextValue(dec)
}

func documentKindFromToken(kind TokenKind) documentValueKind {
	switch kind {
	case TokenKindValueString:
		return documentKindString
	case TokenKindValueInteger:
		return documentKindInteger
	case TokenKindValueFloat:
		return documentKindFloat
	case TokenKindValueBool:
		return documentKindBool
	case TokenKindValueDatetime:
		return documentKindDatetime
	case TokenKindArrayStart:
		return documentKindArray
	case TokenKindInlineTableStart:
		return documentKindInlineTable
	default:
		return documentKindInvalid
	}
}

func (d *Document) formatValueForPath(pathKey string, value any) ([]byte, error) {
	entry, ok := d.entryForPathKey(pathKey)
	if !ok {
		return formatDocumentValue(value)
	}
	if entry.kind == documentKindForGoValue(value) {
		switch entry.kind {
		case documentKindString:
			if s, ok := value.(string); ok {
				return formatStringLike(entry.valueRaw, s)
			}
		case documentKindBool, documentKindInteger, documentKindFloat, documentKindDatetime:
			return formatDocumentValue(value)
		}
	}
	return formatDocumentValue(value)
}

func documentKindForGoValue(value any) documentValueKind {
	switch value.(type) {
	case string:
		return documentKindString
	case bool:
		return documentKindBool
	case float32, float64:
		return documentKindFloat
	case time.Time, LocalDateTime, LocalDate, LocalTime:
		return documentKindDatetime
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return documentKindInvalid
	}
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return documentKindInteger
	case reflect.Slice, reflect.Array:
		return documentKindArray
	case reflect.Map, reflect.Struct:
		return documentKindInlineTable
	default:
		return documentKindInvalid
	}
}

func formatDocumentKeyValue(path []string, value any) ([]byte, error) {
	valueBytes, err := formatDocumentValue(value)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for i, part := range path {
		if i > 0 {
			buf.WriteByte('.')
		}
		if err := writeKey(&buf, part); err != nil {
			return nil, err
		}
	}
	buf.WriteString(" = ")
	buf.Write(valueBytes)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func formatDocumentValue(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeValue(&buf, reflect.ValueOf(value)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func formatStringLike(existing []byte, value string) ([]byte, error) {
	trimmed := bytes.TrimSpace(existing)
	if len(trimmed) >= 2 && trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'' &&
		literalStringSafe(value) {
		return []byte("'" + value + "'"), nil
	}
	return appendBasicString(nil, value)
}

// literalStringSafe reports whether value can be carried verbatim inside a
// single-line TOML literal string: no single quote, no control characters
// other than tab (including DEL and the C1 range, which appendBasicString
// also escapes), and valid UTF-8.
func literalStringSafe(s string) bool {
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c == '\'' || c == 0x7f || (c < 0x20 && c != '\t') {
				return false
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || r <= 0x9f {
			return false
		}
		i += size
	}
	return true
}

func (d *Document) entryForPathKey(pathKey string) (*documentEntry, bool) {
	if d == nil {
		return nil, false
	}
	idx, ok := d.byPath[pathKey]
	if !ok || idx < 0 || idx >= len(d.entries) {
		return nil, false
	}
	return &d.entries[idx], true
}

func parseDocumentPath(path string) ([]string, error) {
	return parseDottedKey([]byte(path))
}

func joinDocumentPath(parts []string) string {
	return strings.Join(parts, "\x00")
}

func documentEntryHint(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	hint := bytes.Count(data, []byte("\n")) + 1
	maxReasonable := len(data)/4 + 1
	if hint > maxReasonable {
		hint = maxReasonable
	}
	return hint
}

func lineSpan(data []byte, start, end int) [2]int {
	lineStart := start
	for lineStart > 0 && data[lineStart-1] != '\n' && data[lineStart-1] != '\r' {
		lineStart--
	}
	lineEnd := end
	for lineEnd < len(data) {
		b := data[lineEnd]
		lineEnd++
		if b == '\n' {
			break
		}
		if b == '\r' {
			if lineEnd < len(data) && data[lineEnd] == '\n' {
				lineEnd++
			}
			break
		}
	}
	return [2]int{lineStart, lineEnd}
}

func documentOpsDelta(ops []documentOp) int {
	delta := 0
	for _, op := range ops {
		delta += len(op.text) - (op.end - op.start)
	}
	if delta < 0 {
		return 0
	}
	return delta
}
