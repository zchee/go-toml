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
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/zchee/go-toml/internal/reflectcache"
)

var errDirectUnknownField = errors.New("toml: direct destination field is unknown")

var directStructEligibilityCache sync.Map // map[reflect.Type]bool

type directAssignment struct {
	name      string
	rawName   []byte
	dst       reflect.Value
	valueKind directValueKind
	mapTarget bool
}

type directValueKind uint8

const (
	directValueGeneric directValueKind = iota
	directValueString
	directValueBool
	directValueInt
	directValueUint
	directValueFloat
	directValueTime
)

// directRawPath keeps common dotted bare-key paths on the stack. It is used
// only while binding the current input buffer, so its byte slices must not be
// stored beyond the active Unmarshal call.
type directRawPath struct {
	stack [8][]byte
	extra [][]byte
	n     int
}

type directPathState struct {
	raw        directRawPath
	text       []string
	valid      bool
	arrayIndex int
}

func directStructUnmarshalEligible(t reflect.Type) (bool, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false, nil
	}
	if t == reflect.TypeFor[time.Time]() ||
		t == reflect.TypeFor[LocalDateTime]() ||
		t == reflect.TypeFor[LocalDate]() ||
		t == reflect.TypeFor[LocalTime]() {
		return false, nil
	}
	if cached, ok := directStructEligibilityCache.Load(t); ok {
		return cached.(bool), nil
	}
	containsMap, err := directTypeContainsMap(t, make(map[reflect.Type]bool))
	if err != nil {
		return false, err
	}
	eligible := !containsMap
	if cached, loaded := directStructEligibilityCache.LoadOrStore(t, eligible); loaded {
		return cached.(bool), nil
	}
	return eligible, nil
}

func directTypeContainsMap(t reflect.Type, seen map[reflect.Type]bool) (bool, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[time.Time]() ||
		t == reflect.TypeFor[LocalDateTime]() ||
		t == reflect.TypeFor[LocalDate]() ||
		t == reflect.TypeFor[LocalTime]() {
		return false, nil
	}
	switch t.Kind() {
	case reflect.Map:
		return true, nil
	case reflect.Struct:
		if seen[t] {
			return false, nil
		}
		seen[t] = true
		info, err := reflectcache.Lookup(t)
		if err != nil {
			return false, normalizeReflectcacheError(err)
		}
		for _, field := range info.Fields {
			containsMap, err := directTypeContainsMap(field.Type, seen)
			if err != nil {
				return false, err
			}
			if containsMap {
				return true, nil
			}
		}
		return false, nil
	case reflect.Slice, reflect.Array:
		return directTypeContainsMap(t.Elem(), seen)
	default:
		return false, nil
	}
}

// bindDocumentDirect decodes the token stream directly into dst. It avoids the
// intermediate documentMap used by generic callers while preserving the same
// Decoder.ReadToken source of truth as the rest of the facade.
//
//nolint:cyclop,funlen,gocognit,gocyclo // direct-decode token dispatch with table/array bookkeeping; cohesive state machine.
func bindDocumentDirect(data []byte, dst reflect.Value, opts []Option, cfg bindConfig) error {
	dec := NewDecoderBytes(data, decoderOptionsWithoutTokenPositions(opts)...)
	current := dst
	currentInfo, err := directStructInfo(current)
	if err != nil {
		return err
	}
	currentPath := directPathState{}
	for {
		tok, err := dec.readToken()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch tok.Kind {
		case TokenKindComment:
			continue
		case TokenKindTableHeader:
			if path, ok := parseDirectHeaderPath(tok.Bytes, false); ok {
				next, err := directTableRaw(dst, path)
				if err != nil {
					return err
				}
				current = next
				currentInfo, err = directStructInfo(current)
				if err != nil {
					return err
				}
				currentPath = directPathState{raw: path, valid: true, arrayIndex: -1}
				continue
			}
			path, err := parseHeaderKey(tok.Bytes, false)
			if err != nil {
				return err
			}
			next, err := directTable(dst, path)
			if err != nil {
				return err
			}
			current = next
			currentInfo, err = directStructInfo(current)
			if err != nil {
				return err
			}
			currentPath = directPathState{text: path, valid: true, arrayIndex: -1}
		case TokenKindArrayTableHeader:
			if path, ok := parseDirectHeaderPath(tok.Bytes, true); ok {
				next, index, err := directArrayTableRaw(dst, path, data, tok.Bytes)
				if err != nil {
					return err
				}
				current = next
				currentInfo, err = directStructInfo(current)
				if err != nil {
					return err
				}
				currentPath = directPathState{raw: path, valid: true, arrayIndex: index}
				continue
			}
			path, err := parseHeaderKey(tok.Bytes, true)
			if err != nil {
				return err
			}
			next, index, err := directArrayTable(dst, path, data, tok.Bytes)
			if err != nil {
				return err
			}
			current = next
			currentInfo, err = directStructInfo(current)
			if err != nil {
				return err
			}
			currentPath = directPathState{text: path, valid: true, arrayIndex: index}
		case TokenKindKey:
			if isSimpleBareKey(tok.Bytes) { //nolint:nestif // bare-key fast path with map/struct target resolution; cohesive.
				target, ok, err := directAssignmentForKeyInfo(current, currentInfo, tok.Bytes)
				if err != nil {
					return err
				}
				if !ok {
					if err := skipNextValue(dec); err != nil {
						return err
					}
					continue
				}
				if err := target.assignFromDecoder(dec, cfg); err != nil {
					return bindErrorPath(err, currentPath.string())
				}
				continue
			}
			if path, ok := parseDirectRawPath(tok.Bytes); ok { //nolint:nestif // raw dotted-path destination resolution; cohesive.
				dst, valueKind, ok, err := directDestinationRaw(current, path)
				if err != nil {
					return err
				}
				if !ok {
					if err := skipNextValue(dec); err != nil {
						return err
					}
					continue
				}
				if dst.IsValid() {
					err = directBindRawFromDecoder(dec, dst, valueKind, path, cfg)
				} else {
					value, parseErr := parseNextValue(dec)
					if parseErr != nil {
						return parseErr
					}
					err = directAssignRaw(current, path, value, cfg)
				}
				if err != nil {
					return bindErrorPath(err, currentPath.string())
				}
				continue
			}
			key, err := parseDottedKey(tok.Bytes)
			if err != nil {
				return err
			}
			dst, valueKind, ok, err := directDestination(current, key)
			if err != nil {
				return err
			}
			if !ok {
				if err := skipNextValue(dec); err != nil {
					return err
				}
				continue
			}
			if dst.IsValid() {
				err = directBindFromDecoder(dec, dst, valueKind, key, cfg)
			} else {
				value, parseErr := parseNextValue(dec)
				if parseErr != nil {
					return parseErr
				}
				err = directAssign(current, key, value, cfg)
			}
			if err != nil {
				return bindErrorPath(err, currentPath.string())
			}
		default:
			return decoderSyntaxErrorForRawToken(dec, tok, "unexpected token")
		}
	}
}

func directStructInfo(v reflect.Value) (*reflectcache.TypeInfo, error) {
	v = directWritableValue(v)
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return nil, nil //nolint:nilnil // a nil TypeInfo means "not a struct"; callers treat it as absent field metadata.
	}
	info, err := reflectcache.Lookup(v.Type())
	if err != nil {
		return nil, normalizeReflectcacheError(err)
	}
	return info, nil
}

func directValueKindOfType(t reflect.Type) directValueKind {
	if t == reflect.TypeFor[time.Time]() {
		return directValueTime
	}
	switch t.Kind() {
	case reflect.String:
		return directValueString
	case reflect.Bool:
		return directValueBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return directValueInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return directValueUint
	case reflect.Float32, reflect.Float64:
		return directValueFloat
	default:
		return directValueGeneric
	}
}

func parseDirectHeaderPath(raw []byte, array bool) (directRawPath, bool) {
	return parseDirectRawPath(trimHeaderKey(raw, array))
}

// parseDirectRawPath accepts the allocation-free subset used by most decoded
// keys and table headers. Quoted or whitespace-heavy paths deliberately return
// ok=false so the full parser remains the compatibility path.
func parseDirectRawPath(raw []byte) (directRawPath, bool) {
	raw = bytes.TrimSpace(raw)
	var path directRawPath
	for len(raw) > 0 {
		end := 0
		for end < len(raw) && raw[end] != '.' {
			if !isDirectBareKeyByte(raw[end]) {
				return directRawPath{}, false
			}
			end++
		}
		if end == 0 {
			return directRawPath{}, false
		}
		path.append(raw[:end])
		if end == len(raw) {
			return path, true
		}
		raw = raw[end+1:]
	}
	return directRawPath{}, false
}

func isDirectBareKeyByte(c byte) bool {
	return c >= 'A' && c <= 'Z' ||
		c >= 'a' && c <= 'z' ||
		c >= '0' && c <= '9' ||
		c == '_' ||
		c == '-'
}

func (p *directRawPath) append(part []byte) {
	if p.n < len(p.stack) {
		p.stack[p.n] = part
	} else {
		p.extra = append(p.extra, part)
	}
	p.n++
}

func (p directRawPath) len() int {
	return p.n
}

func (p directRawPath) part(i int) []byte {
	if i < len(p.stack) {
		return p.stack[i]
	}
	return p.extra[i-len(p.stack)]
}

func (p directRawPath) stringAt(i int) string {
	return string(p.part(i))
}

func (p directRawPath) string() string {
	switch p.n {
	case 0:
		return ""
	case 1:
		return p.stringAt(0)
	}
	var b strings.Builder
	for i := range p.n {
		if i > 0 {
			b.WriteByte('.')
		}
		b.Write(p.part(i))
	}
	return b.String()
}

func (s directPathState) string() string {
	if !s.valid {
		return ""
	}
	base := s.stringNoIndex()
	if s.arrayIndex < 0 {
		return base
	}
	var buf [32]byte
	b := buf[:0]
	b = append(b, base...)
	b = append(b, '[')
	b = strconv.AppendInt(b, int64(s.arrayIndex), 10)
	b = append(b, ']')
	return string(b)
}

func (s directPathState) stringNoIndex() string {
	if len(s.text) > 0 {
		return strings.Join(s.text, ".")
	}
	return s.raw.string()
}

func directAssignmentForKey(root reflect.Value, raw []byte) (directAssignment, bool, error) {
	return directAssignmentForKeyInfo(root, nil, raw)
}

func directAssignmentForKeyInfo(root reflect.Value, info *reflectcache.TypeInfo, raw []byte) (directAssignment, bool, error) {
	if !root.IsValid() {
		return directAssignment{}, false, nil
	}
	cur := directWritableValue(root)
	switch cur.Kind() {
	case reflect.Struct:
		if info == nil || info.Type != cur.Type() {
			var err error
			info, err = reflectcache.Lookup(cur.Type())
			if err != nil {
				return directAssignment{}, false, normalizeReflectcacheError(err)
			}
		}
		field, ok := lookupStructFieldBytes(info, raw)
		if !ok {
			return directAssignment{}, false, nil
		}
		return directAssignment{rawName: raw, dst: cur.FieldByIndex(field.Index), valueKind: directValueKindOfType(field.Type)}, true, nil
	case reflect.Map:
		if cur.Type().Key().Kind() != reflect.String {
			return directAssignment{}, false, &UnsupportedTypeError{Type: cur.Type().String()}
		}
		if cur.IsNil() {
			cur.Set(reflect.MakeMap(cur.Type()))
		}
		return directAssignment{name: string(raw), dst: cur, mapTarget: true}, true, nil
	default:
		return directAssignment{}, false, nil
	}
}

func lookupStructFieldBytes(info *reflectcache.TypeInfo, name []byte) (reflectcache.Field, bool) {
	if field, ok := info.ByName[unsafeString(name)]; ok {
		return field, true
	}
	for _, field := range info.Fields {
		if asciiEqualFoldStringBytes(field.Name, name) {
			return field, true
		}
	}
	return reflectcache.Field{}, false
}

func asciiEqualFoldStringBytes(s string, b []byte) bool {
	if len(s) != len(b) {
		return false
	}
	for i := range s {
		if asciiFoldByte(s[i]) != asciiFoldByte(b[i]) {
			return false
		}
	}
	return true
}

func asciiFoldByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	// The result is used only for immediate map lookups against cached struct
	// metadata. Callers must not store it because it aliases the input buffer.
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func (a directAssignment) assign(value any, cfg bindConfig) error {
	if a.mapTarget {
		elem := reflect.New(a.dst.Type().Elem()).Elem()
		if err := bindValue(elem, value, cfg); err != nil {
			return bindErrorPath(err, a.pathName())
		}
		a.dst.SetMapIndex(reflect.ValueOf(a.name).Convert(a.dst.Type().Key()), elem)
		return nil
	}
	if err := bindValue(a.dst, value, cfg); err != nil {
		return bindErrorPath(err, a.pathName())
	}
	return nil
}

func (a directAssignment) assignFromDecoder(dec *Decoder, cfg bindConfig) error {
	if a.mapTarget {
		value, err := parseNextValue(dec)
		if err != nil {
			return err
		}
		return a.assign(value, cfg)
	}
	if err := directBindFromDecoderNoPath(dec, a.dst, a.valueKind, cfg); err != nil {
		return bindErrorPath(err, a.pathName())
	}
	return nil
}

func (a directAssignment) pathName() string {
	if a.name != "" {
		return a.name
	}
	return string(a.rawName)
}

func directTableRaw(root reflect.Value, path directRawPath) (reflect.Value, error) {
	return directTableRawN(root, path, path.len())
}

func directTableRawN(root reflect.Value, path directRawPath, n int) (reflect.Value, error) {
	cur := root
	for i := range n {
		cur = directWritableValue(cur)
		if cur.IsValid() && cur.Kind() == reflect.Slice {
			if cur.Len() == 0 {
				return reflect.Value{}, nil
			}
			cur = directWritableValue(cur.Index(cur.Len() - 1))
		}
		next, err := directFieldRaw(cur, path.part(i))
		if errors.Is(err, errDirectUnknownField) {
			return reflect.Value{}, nil
		}
		if err != nil {
			return reflect.Value{}, err
		}
		cur = directWritableValue(next)
	}
	if cur = directWritableValue(cur); cur.IsValid() && cur.Kind() == reflect.Slice {
		if cur.Len() == 0 {
			return reflect.Value{}, nil
		}
		cur = directWritableValue(cur.Index(cur.Len() - 1))
	}
	return cur, nil
}

func directTable(root reflect.Value, path []string) (reflect.Value, error) {
	cur := root
	for _, name := range path {
		cur = directWritableValue(cur)
		if cur.IsValid() && cur.Kind() == reflect.Slice {
			if cur.Len() == 0 {
				return reflect.Value{}, nil
			}
			cur = directWritableValue(cur.Index(cur.Len() - 1))
		}
		next, err := directField(cur, name)
		if errors.Is(err, errDirectUnknownField) {
			return reflect.Value{}, nil
		}
		if err != nil {
			return reflect.Value{}, err
		}
		cur = directWritableValue(next)
	}
	if cur = directWritableValue(cur); cur.IsValid() && cur.Kind() == reflect.Slice {
		if cur.Len() == 0 {
			return reflect.Value{}, nil
		}
		cur = directWritableValue(cur.Index(cur.Len() - 1))
	}
	return cur, nil
}

func directDestinationRaw(root reflect.Value, path directRawPath) (reflect.Value, directValueKind, bool, error) {
	if !root.IsValid() {
		return reflect.Value{}, directValueGeneric, false, nil
	}
	cur := root
	for i := range path.len() {
		cur = directWritableValue(cur)
		switch cur.Kind() {
		case reflect.Struct:
			info, err := reflectcache.Lookup(cur.Type())
			if err != nil {
				return reflect.Value{}, directValueGeneric, false, normalizeReflectcacheError(err)
			}
			field, ok := lookupStructFieldBytes(info, path.part(i))
			if !ok {
				return reflect.Value{}, directValueGeneric, false, nil
			}
			cur = cur.FieldByIndex(field.Index)
		case reflect.Map:
			return reflect.Value{}, directValueGeneric, true, nil
		default:
			return reflect.Value{}, directValueGeneric, false, nil
		}
	}
	return cur, directValueKindOfType(cur.Type()), true, nil
}

func directDestination(root reflect.Value, path []string) (reflect.Value, directValueKind, bool, error) {
	if !root.IsValid() {
		return reflect.Value{}, directValueGeneric, false, nil
	}
	cur := root
	for _, name := range path {
		cur = directWritableValue(cur)
		switch cur.Kind() {
		case reflect.Struct:
			info, err := reflectcache.Lookup(cur.Type())
			if err != nil {
				return reflect.Value{}, directValueGeneric, false, normalizeReflectcacheError(err)
			}
			field, ok := lookupStructField(info, name)
			if !ok {
				return reflect.Value{}, directValueGeneric, false, nil
			}
			cur = cur.FieldByIndex(field.Index)
		case reflect.Map:
			return reflect.Value{}, directValueGeneric, true, nil
		default:
			return reflect.Value{}, directValueGeneric, false, nil
		}
	}
	return cur, directValueKindOfType(cur.Type()), true, nil
}

func directArrayTableRaw(root reflect.Value, path directRawPath, data, header []byte) (reflect.Value, int, error) {
	if path.len() == 0 {
		return root, -1, nil
	}
	parent, err := directTableRawN(root, path, path.len()-1)
	if err != nil {
		return reflect.Value{}, -1, err
	}
	if !parent.IsValid() {
		return reflect.Value{}, -1, nil
	}
	slot, err := directFieldRaw(parent, path.part(path.len()-1))
	if errors.Is(err, errDirectUnknownField) {
		return reflect.Value{}, -1, nil
	}
	if err != nil {
		return reflect.Value{}, -1, err
	}
	slot = directWritableValue(slot)
	if slot.Kind() != reflect.Slice {
		return reflect.Value{}, -1, bindDirectRawErrorPath(mismatch(slot.Type(), []any{}), path)
	}
	capacityHint := directArrayTableCapacityHint(data, header, path.len(), slot.Len())
	elem, index := appendDirectSliceElement(slot, capacityHint)
	return elem, index, nil
}

func directArrayTable(root reflect.Value, path []string, data, header []byte) (reflect.Value, int, error) {
	if len(path) == 0 {
		return root, -1, nil
	}
	parent, err := directTable(root, path[:len(path)-1])
	if err != nil {
		return reflect.Value{}, -1, err
	}
	if !parent.IsValid() {
		return reflect.Value{}, -1, nil
	}
	slot, err := directField(parent, path[len(path)-1])
	if errors.Is(err, errDirectUnknownField) {
		return reflect.Value{}, -1, nil
	}
	if err != nil {
		return reflect.Value{}, -1, err
	}
	slot = directWritableValue(slot)
	if slot.Kind() != reflect.Slice {
		return reflect.Value{}, -1, bindDirectErrorPath(mismatch(slot.Type(), []any{}), path)
	}
	capacityHint := directArrayTableCapacityHint(data, header, len(path), slot.Len())
	elem, index := appendDirectSliceElement(slot, capacityHint)
	return elem, index, nil
}

func directArrayTableCapacityHint(data, header []byte, pathLen, currentLen int) int {
	if pathLen != 1 || currentLen != 0 {
		return 0
	}
	return bytes.Count(data, header)
}

func appendDirectSliceElement(slot reflect.Value, capacityHint int) (elem reflect.Value, index int) {
	index = slot.Len()
	if index == 0 && capacityHint > 0 {
		slot.Grow(capacityHint)
	} else {
		slot.Grow(1)
	}
	slot.SetLen(index + 1)
	elem = slot.Index(index)
	elem.SetZero()
	return directWritableValue(elem), index
}

func directAssignRaw(root reflect.Value, path directRawPath, value any, cfg bindConfig) error {
	if !root.IsValid() {
		return nil
	}
	if path.len() == 0 {
		return &SyntaxError{Line: 1, Col: 1, Msg: "empty key", Span: [2]int{0, 0}}
	}
	cur := root
	for i := range path.len() - 1 {
		next, err := directFieldRaw(cur, path.part(i))
		if errors.Is(err, errDirectUnknownField) {
			return nil
		}
		if err != nil {
			return err
		}
		cur = directWritableValue(next)
	}
	cur = directWritableValue(cur)
	if cur.Kind() == reflect.Map {
		return directAssignMapRaw(cur, path, value, cfg)
	}
	dst, err := directFieldRaw(cur, path.part(path.len()-1))
	if errors.Is(err, errDirectUnknownField) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := bindValue(dst, value, cfg); err != nil {
		return bindDirectRawErrorPath(err, path)
	}
	return nil
}

func directBindRawFromDecoder(dec *Decoder, dst reflect.Value, valueKind directValueKind, path directRawPath, cfg bindConfig) error {
	if err := directBindFromDecoderNoPath(dec, dst, valueKind, cfg); err != nil {
		return bindDirectRawErrorPath(err, path)
	}
	return nil
}

func directAssign(root reflect.Value, path []string, value any, cfg bindConfig) error {
	if !root.IsValid() {
		return nil
	}
	if len(path) == 0 {
		return &SyntaxError{Line: 1, Col: 1, Msg: "empty key", Span: [2]int{0, 0}}
	}
	cur := root
	for _, name := range path[:len(path)-1] {
		next, err := directField(cur, name)
		if errors.Is(err, errDirectUnknownField) {
			return nil
		}
		if err != nil {
			return err
		}
		cur = directWritableValue(next)
	}
	cur = directWritableValue(cur)
	if cur.Kind() == reflect.Map {
		return directAssignMap(cur, path, value, cfg)
	}
	dst, err := directField(cur, path[len(path)-1])
	if errors.Is(err, errDirectUnknownField) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := bindValue(dst, value, cfg); err != nil {
		return bindDirectErrorPath(err, path)
	}
	return nil
}

func directBindFromDecoder(dec *Decoder, dst reflect.Value, valueKind directValueKind, path []string, cfg bindConfig) error {
	if err := directBindFromDecoderNoPath(dec, dst, valueKind, cfg); err != nil {
		return bindDirectErrorPath(err, path)
	}
	return nil
}

func directBindFromDecoderNoPath(dec *Decoder, dst reflect.Value, valueKind directValueKind, cfg bindConfig) error {
	tok, err := directNextValueToken(dec)
	if err != nil {
		return err
	}
	return directBindTypedToken(dec, tok, dst, valueKind, cfg)
}

func directNextValueToken(dec *Decoder) (rawToken, error) {
	for {
		tok, err := dec.readToken()
		if err != nil {
			return rawToken{}, err
		}
		if tok.Kind == TokenKindComment {
			continue
		}
		return tok, nil
	}
}

func directStringValue(dec *Decoder, raw []byte, cfg bindConfig) (string, error) {
	if dec == nil || cfg.copyStrings {
		return parseStringValue(raw)
	}
	body, kind, err := stringValueBody(raw)
	if err != nil {
		return "", err
	}
	switch kind {
	case stringValueLiteral:
		return directLiteralStringValue(dec, raw, body, false)
	case stringValueMultilineLiteral:
		return directLiteralStringValue(dec, raw, body, true)
	case stringValueBasic:
		return directBasicStringValue(dec, raw, body, false)
	case stringValueMultilineBasic:
		return directBasicStringValue(dec, raw, body, true)
	default:
		return "", malformedStringError(raw)
	}
}

func directLiteralStringValue(dec *Decoder, raw, body []byte, multiline bool) (string, error) {
	if err := validateLiteralStringBody(body, multiline); err != nil {
		return "", err
	}
	if s, ok := dec.arenaString(body); ok {
		return s, nil
	}
	return parseStringValue(raw)
}

func directBasicStringValue(dec *Decoder, raw, body []byte, multiline bool) (string, error) {
	if bytes.IndexByte(body, '\\') >= 0 {
		return parseStringValue(raw)
	}
	if err := validateBasicStringBody(body, multiline); err != nil {
		return "", err
	}
	if s, ok := dec.arenaString(body); ok {
		return s, nil
	}
	return parseStringValue(raw)
}

func (d *Decoder) arenaString(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	if d == nil || len(d.buf) == 0 || len(raw) > len(d.buf) {
		return "", false
	}
	base := uintptr(unsafe.Pointer(unsafe.SliceData(d.buf)))
	ptr := uintptr(unsafe.Pointer(unsafe.SliceData(raw)))
	if ptr < base {
		return "", false
	}
	off := ptr - base
	if off > uintptr(len(d.buf)-len(raw)) {
		return "", false
	}
	// raw is proven to lie within d.buf, so the decoded string can alias the
	// input buffer directly with zero copy. This is the default aliasing
	// contract: decoded strings remain valid only while the input buffer is
	// unmodified; WithCopiedStrings opts into independent copies (handled in
	// directStringValue before this function is reached).
	return unsafe.String(&d.buf[off], len(raw)), true
}

//nolint:cyclop,funlen,gocognit // typed fast-path bind dispatch over direct value kinds; cohesive.
func directBindTypedToken(dec *Decoder, tok rawToken, dst reflect.Value, valueKind directValueKind, cfg bindConfig) error {
	if !dst.CanSet() {
		return nil
	}
	switch valueKind {
	case directValueString:
		if tok.Kind == TokenKindValueString {
			s, err := directStringValue(dec, tok.Bytes, cfg)
			if err != nil {
				return err
			}
			dst.SetString(s)
			return nil
		}
	case directValueBool:
		if tok.Kind == TokenKindValueBool {
			switch {
			case bytes.Equal(tok.Bytes, trueLiteral):
				dst.SetBool(true)
				return nil
			case bytes.Equal(tok.Bytes, falseLiteral):
				dst.SetBool(false)
				return nil
			default:
				return decoderSyntaxErrorForRawToken(dec, tok, "malformed boolean")
			}
		}
	case directValueInt:
		if tok.Kind == TokenKindValueInteger {
			i, err := rawTokenIntegerValue(dec, tok)
			if err != nil {
				return err
			}
			if dst.OverflowInt(i) {
				return mismatch(dst.Type(), i)
			}
			dst.SetInt(i)
			return nil
		}
	case directValueUint:
		if tok.Kind == TokenKindValueInteger {
			i, err := rawTokenIntegerValue(dec, tok)
			if err != nil {
				return err
			}
			if i < 0 || dst.OverflowUint(uint64(i)) {
				return mismatch(dst.Type(), i)
			}
			dst.SetUint(uint64(i))
			return nil
		}
	case directValueFloat:
		switch tok.Kind {
		case TokenKindValueFloat:
			f, err := rawTokenFloatValue(dec, tok)
			if err != nil {
				return err
			}
			if dst.OverflowFloat(f) {
				return mismatch(dst.Type(), f)
			}
			dst.SetFloat(f)
			return nil
		case TokenKindValueInteger:
			i, err := rawTokenIntegerValue(dec, tok)
			if err != nil {
				return err
			}
			f := float64(i)
			if dst.OverflowFloat(f) {
				return mismatch(dst.Type(), i)
			}
			dst.SetFloat(f)
			return nil
		}
	case directValueTime:
		if tok.Kind == TokenKindValueDatetime {
			return directBindTimeToken(tok, dst, cfg)
		}
	}
	value, err := parseValueToken(dec, tok.publicToken())
	if err != nil {
		return err
	}
	return bindValue(dst, value, cfg)
}

func directBindTimeToken(tok rawToken, dst reflect.Value, cfg bindConfig) error {
	v, kind, err := parseDateTimeValue(tok.Bytes)
	if err != nil {
		return err
	}
	if kind == dateTimeKindOffset {
		dst.Set(reflect.ValueOf(v.(time.Time)))
		return nil
	}
	if !cfg.localAsUTC {
		return &LocalTimeIntoTimeError{Kind: TokenKindValueDatetime}
	}
	switch x := v.(type) {
	case LocalDateTime:
		dst.Set(reflect.ValueOf(time.Date(x.Year, time.Month(x.Month), x.Day, x.Hour, x.Minute, x.Second, x.Nanosecond, time.UTC)))
	case LocalDate:
		dst.Set(reflect.ValueOf(time.Date(x.Year, time.Month(x.Month), x.Day, 0, 0, 0, 0, time.UTC)))
	case LocalTime:
		dst.Set(reflect.ValueOf(time.Date(0, time.January, 1, x.Hour, x.Minute, x.Second, x.Nanosecond, time.UTC)))
	default:
		return mismatch(dst.Type(), v)
	}
	return nil
}

func directFieldRaw(container reflect.Value, name []byte) (reflect.Value, error) {
	container = directWritableValue(container)
	switch container.Kind() {
	case reflect.Struct:
		info, err := reflectcache.Lookup(container.Type())
		if err != nil {
			return reflect.Value{}, normalizeReflectcacheError(err)
		}
		field, ok := lookupStructFieldBytes(info, name)
		if !ok {
			return reflect.Value{}, errDirectUnknownField
		}
		return container.FieldByIndex(field.Index), nil
	case reflect.Map:
		if container.Type().Key().Kind() != reflect.String {
			return reflect.Value{}, &UnsupportedTypeError{Type: container.Type().String()}
		}
		if container.IsNil() {
			container.Set(reflect.MakeMap(container.Type()))
		}
		elem := reflect.New(container.Type().Elem()).Elem()
		if elem.Kind() == reflect.Map {
			elem.Set(reflect.MakeMap(elem.Type()))
		}
		return elem, nil
	default:
		return reflect.Value{}, &UnsupportedTypeError{Type: container.Type().String()}
	}
}

func directField(container reflect.Value, name string) (reflect.Value, error) {
	container = directWritableValue(container)
	switch container.Kind() {
	case reflect.Struct:
		info, err := reflectcache.Lookup(container.Type())
		if err != nil {
			return reflect.Value{}, normalizeReflectcacheError(err)
		}
		field, ok := lookupStructField(info, name)
		if !ok {
			return reflect.Value{}, errDirectUnknownField
		}
		return container.FieldByIndex(field.Index), nil
	case reflect.Map:
		if container.Type().Key().Kind() != reflect.String {
			return reflect.Value{}, &UnsupportedTypeError{Type: container.Type().String()}
		}
		if container.IsNil() {
			container.Set(reflect.MakeMap(container.Type()))
		}
		elem := reflect.New(container.Type().Elem()).Elem()
		if elem.Kind() == reflect.Map {
			elem.Set(reflect.MakeMap(elem.Type()))
		}
		return elem, nil
	default:
		return reflect.Value{}, &UnsupportedTypeError{Type: container.Type().String()}
	}
}

func directAssignMapRaw(dst reflect.Value, path directRawPath, value any, cfg bindConfig) error {
	if dst.Type().Key().Kind() != reflect.String {
		return &UnsupportedTypeError{Type: dst.Type().String()}
	}
	if dst.IsNil() {
		dst.Set(reflect.MakeMap(dst.Type()))
	}
	elem := reflect.New(dst.Type().Elem()).Elem()
	if err := bindValue(elem, value, cfg); err != nil {
		return bindDirectRawErrorPath(err, path)
	}
	name := path.stringAt(path.len() - 1)
	dst.SetMapIndex(reflect.ValueOf(name).Convert(dst.Type().Key()), elem)
	return nil
}

func directAssignMap(dst reflect.Value, path []string, value any, cfg bindConfig) error {
	if dst.Type().Key().Kind() != reflect.String {
		return &UnsupportedTypeError{Type: dst.Type().String()}
	}
	if dst.IsNil() {
		dst.Set(reflect.MakeMap(dst.Type()))
	}
	elem := reflect.New(dst.Type().Elem()).Elem()
	if err := bindValue(elem, value, cfg); err != nil {
		return bindDirectErrorPath(err, path)
	}
	name := path[len(path)-1]
	dst.SetMapIndex(reflect.ValueOf(name).Convert(dst.Type().Key()), elem)
	return nil
}

func directWritableValue(v reflect.Value) reflect.Value {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	return v
}

func bindDirectErrorPath(err error, path []string) error {
	for _, p := range slices.Backward(path) {
		err = bindErrorPath(err, p)
	}
	return err
}

func bindDirectRawErrorPath(err error, path directRawPath) error {
	for i := path.len() - 1; i >= 0; i-- {
		err = bindErrorPath(err, path.stringAt(i))
	}
	return err
}
