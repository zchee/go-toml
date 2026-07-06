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
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/zchee/go-toml/internal/scan"
)

type documentMap = map[string]any

const documentMapHint = 4

var (
	trueLiteral   = []byte("true")
	falseLiteral  = []byte("false")
	infLiteral    = []byte("inf")
	posInfLiteral = []byte("+inf")
	negInfLiteral = []byte("-inf")
	nanLiteral    = []byte("nan")
	posNanLiteral = []byte("+nan")
	negNanLiteral = []byte("-nan")
)

var documentMapPool = sync.Pool{
	New: func() any {
		return make(documentMap, documentMapHint)
	},
}

//nolint:cyclop,funlen,gocognit,gocyclo // top-level TOML statement dispatch with table/array-table bookkeeping; cohesive state machine.
func parseDocument(data []byte, opts []Option, filter *decodeFilter) (documentMap, error) {
	dec := NewDecoderBytes(data, decoderOptionsWithoutTokenPositions(opts)...)
	root := newDocumentMap()
	current := root
	currentPath := []string(nil)
	currentFilter := filter
	inTable := false
	var declaredTables map[string]string
	var dottedKeyTables map[string]string
	var arrayTables map[string]struct{}
	var arrayTableEpochs map[string]int
	var closedInlineTables map[string]string
	for {
		tok, err := dec.readToken()
		if errors.Is(err, io.EOF) {
			return root, nil
		}
		if err != nil {
			return nil, err
		}
		switch tok.Kind {
		case TokenKindComment:
			continue
		case TokenKindTableHeader:
			key := trimHeaderKey(tok.Bytes, false)
			path, err := keyPath(key)
			if err != nil {
				return nil, err
			}
			pathKey := strings.Join(path, ".")
			encodedPathKey := encodedKeyPath(path)
			declContext := declarationContext(path, arrayTableEpochs)
			if _, ok := arrayTables[encodedPathKey]; ok {
				return nil, semanticSyntaxErrorRaw(data, tok, "cannot redefine array table as table")
			}
			if existingContext, ok := dottedKeyTables[encodedPathKey]; ok && existingContext == declContext {
				return nil, semanticSyntaxErrorRaw(data, tok, "cannot redefine dotted key table")
			}
			if closedInlinePrefix(path, closedInlineTables, arrayTableEpochs) {
				return nil, semanticSyntaxErrorRaw(data, tok, "cannot extend inline table")
			}
			if existingContext, ok := declaredTables[encodedPathKey]; ok && existingContext == declContext {
				return nil, semanticSyntaxErrorRaw(data, tok, "duplicate table")
			}
			if nextFilter, ok := filter.lookupPath(path); ok {
				next, err := ensureTableRaw(data, root, path, tok)
				if err != nil {
					return nil, bindErrorPath(err, pathKey)
				}
				current = next
				currentPath = append(currentPath[:0], path...)
				currentFilter = nextFilter
				if declaredTables == nil {
					declaredTables = make(map[string]string, documentMapHint)
				}
				declaredTables[encodedPathKey] = declContext
			} else {
				current = nil
				currentPath = nil
				currentFilter = nil
			}
			inTable = true
		case TokenKindArrayTableHeader:
			key := trimHeaderKey(tok.Bytes, true)
			path, err := keyPath(key)
			if err != nil {
				return nil, err
			}
			pathKey := strings.Join(path, ".")
			encodedPathKey := encodedKeyPath(path)
			if _, ok := declaredTables[encodedPathKey]; ok {
				return nil, semanticSyntaxErrorRaw(data, tok, "cannot redefine table as array table")
			}
			declContext := declarationContext(path, arrayTableEpochs)
			if existingContext, ok := dottedKeyTables[encodedPathKey]; ok && existingContext == declContext {
				return nil, semanticSyntaxErrorRaw(data, tok, "cannot redefine dotted key table")
			}
			if closedInlinePrefix(path, closedInlineTables, arrayTableEpochs) {
				return nil, semanticSyntaxErrorRaw(data, tok, "cannot extend inline table")
			}
			if arrayTables == nil {
				arrayTables = make(map[string]struct{}, documentMapHint)
			}
			arrayTables[encodedPathKey] = struct{}{}
			if arrayTableEpochs == nil {
				arrayTableEpochs = make(map[string]int, documentMapHint)
			}
			arrayTableEpochs[encodedPathKey]++
			if nextFilter, ok := filter.lookupPath(path); ok { //nolint:nestif // array-table target resolution and capacity hinting; cohesive.
				var next documentMap
				if len(path) == 1 {
					capacityHint := 0
					if _, exists := root[path[0]]; !exists {
						capacityHint = bytes.Count(data, tok.Bytes)
					}
					next, err = appendArrayTableKeyRaw(data, root, path[0], capacityHint, tok)
				} else {
					next, err = appendArrayTableRaw(data, root, path, tok)
				}
				if err != nil {
					return nil, bindErrorPath(err, pathKey)
				}
				current = next
				currentPath = append(currentPath[:0], path...)
				currentFilter = nextFilter
			} else {
				current = nil
				currentPath = nil
				currentFilter = nil
			}
			inTable = true
		case TokenKindKey:
			if current == nil {
				if err := skipNextValue(dec); err != nil {
					return nil, err
				}
				continue
			}
			if _, ok := currentFilter.lookup(tok.Bytes); !ok {
				if err := skipNextValue(dec); err != nil {
					return nil, err
				}
				continue
			}
			key, err := keyPath(tok.Bytes)
			if err != nil {
				return nil, err
			}
			fullPath := append(append([]string(nil), currentPath...), key...)
			if len(key) > 1 {
				for i := len(currentPath) + 1; i < len(fullPath); i++ {
					prefix := encodedKeyPath(fullPath[:i])
					if _, ok := declaredTables[prefix]; ok {
						return nil, semanticSyntaxErrorRaw(data, tok, "cannot extend explicit table with dotted key")
					}
					if existingContext, ok := closedInlineTables[prefix]; ok && existingContext == declarationContext(fullPath[:i], arrayTableEpochs) {
						return nil, semanticSyntaxErrorRaw(data, tok, "cannot extend inline table")
					}
				}
			}
			value, err := parseNextValue(dec)
			if err != nil {
				return nil, err
			}
			if err := assignUniqueRaw(data, current, key, value, tok); err != nil {
				return nil, bindErrorPath(err, strings.Join(fullPath, "."))
			}
			dottedKeyTables = markDottedKeyTables(dottedKeyTables, fullPath, len(currentPath), arrayTableEpochs)
			closedInlineTables = markClosedValuePaths(closedInlineTables, fullPath, value, arrayTableEpochs)
		default:
			if !inTable {
				return nil, syntaxErrorForRawToken(data, tok, "unexpected token")
			}
		}
	}
}

func skipNextValue(dec *Decoder) error {
	if ok, err := skipStructuralValueFast(dec); ok || err != nil {
		return err
	}
	for {
		tok, err := dec.readToken()
		if err != nil {
			return err
		}
		if tok.Kind == TokenKindComment {
			continue
		}
		return skipRawValueToken(dec, tok)
	}
}

func skipStructuralValueFast(dec *Decoder) (bool, error) {
	if dec == nil {
		return false, nil
	}
	dec.skipSpaces()
	if dec.off >= len(dec.buf) || dec.err != nil {
		return false, dec.err
	}
	switch dec.buf[dec.off] {
	case '[', '{':
	default:
		return false, nil
	}
	start := dec.off
	end, err := dec.scanSkippedStructuralValue(start)
	if err != nil {
		return true, err
	}
	dec.advanceBytes(dec.buf[start:end])
	dec.expectingValue = false
	dec.valueNoNewline = false
	if dec.innermostIsArray() {
		dec.expectingValue = true
	}
	dec.needSeparator = len(dec.containerStack) > 0
	dec.needLineEnd = len(dec.containerStack) == 0
	return true, nil
}

type skippedContainerFrame struct {
	kind  byte
	state skippedContainerState
}

type skippedContainerState uint8

const (
	skippedArrayValue skippedContainerState = iota
	skippedArraySeparator
	skippedInlineKey
	skippedInlineValue
	skippedInlineSeparator
)

//nolint:cyclop,funlen,gocognit // container-skip state machine over array/inline frames; cohesive.
func (d *Decoder) scanSkippedStructuralValue(start int) (int, error) {
	var stack [32]skippedContainerFrame
	frames := stack[:0]
	arrayDepth := d.arrayDepth
	inlineDepth := d.inlineDepth
	push := func(kind byte, off int) error {
		state := skippedArrayValue
		if kind == containerInline {
			state = skippedInlineKey
			inlineDepth++
			if d.limits.MaxNestedDepth > 0 && inlineDepth > d.limits.MaxNestedDepth {
				err := &LimitError{Limit: "MaxNestedDepth", Value: d.limits.MaxNestedDepth, Span: [2]int{off, off + 1}}
				d.setErr(err)
				return err
			}
		} else {
			arrayDepth++
			if d.limits.MaxNestedDepth > 0 && arrayDepth > d.limits.MaxNestedDepth {
				err := &LimitError{Limit: "MaxNestedDepth", Value: d.limits.MaxNestedDepth, Span: [2]int{off, off + 1}}
				d.setErr(err)
				return err
			}
		}
		frames = append(frames, skippedContainerFrame{kind: kind, state: state})
		return nil
	}
	pop := func(kind byte) {
		frames = frames[:len(frames)-1]
		if kind == containerInline {
			inlineDepth--
		} else {
			arrayDepth--
		}
	}
	switch d.buf[start] {
	case '[':
		if err := push(containerArray, start); err != nil {
			return 0, err
		}
	case '{':
		if err := push(containerInline, start); err != nil {
			return 0, err
		}
	default:
		return 0, d.syntaxError("expected value", start)
	}
	i := start + 1
	for len(frames) > 0 {
		var err error
		top := len(frames) - 1
		if frames[top].state == skippedInlineValue {
			i = skipSkippedInlineAssignmentSpace(d.buf, i)
		} else {
			i, err = d.skipSkippedStructuralSpace(i)
		}
		if err != nil {
			return 0, err
		}
		if i >= len(d.buf) {
			return 0, d.syntaxError("unterminated value", len(d.buf))
		}
		switch frames[top].state {
		case skippedArrayValue:
			if d.buf[i] == ']' {
				pop(containerArray)
				i++
				continue
			}
			frames[top].state = skippedArraySeparator
			next, err := d.scanSkippedValue(i, push)
			if err != nil {
				return 0, err
			}
			i = next
		case skippedArraySeparator:
			switch d.buf[i] {
			case ',':
				frames[top].state = skippedArrayValue
				i++
			case ']':
				pop(containerArray)
				i++
			default:
				return 0, d.syntaxError("expected array separator", i)
			}
		case skippedInlineKey:
			if d.buf[i] == '}' {
				pop(containerInline)
				i++
				continue
			}
			next, err := d.scanSkippedInlineKey(i)
			if err != nil {
				return 0, err
			}
			frames[top].state = skippedInlineValue
			i = next
		case skippedInlineValue:
			frames[top].state = skippedInlineSeparator
			next, err := d.scanSkippedValue(i, push)
			if err != nil {
				return 0, err
			}
			i = next
		case skippedInlineSeparator:
			switch d.buf[i] {
			case ',':
				frames[top].state = skippedInlineKey
				i++
			case '}':
				pop(containerInline)
				i++
			default:
				return 0, d.syntaxError("expected inline table separator", i)
			}
		}
	}
	return i, nil
}

func (d *Decoder) skipSkippedStructuralSpace(i int) (int, error) {
	for i < len(d.buf) {
		switch d.buf[i] {
		case ' ', '\t', '\n':
			i++
		case '\r':
			if i+1 < len(d.buf) && d.buf[i+1] == '\n' {
				i += 2
				continue
			}
			return 0, d.syntaxError("control character in value", i)
		case '#':
			end, err := d.scanSkippedComment(i)
			if err != nil {
				return 0, err
			}
			i = end
		default:
			return i, nil
		}
	}
	return i, nil
}

func skipSkippedInlineAssignmentSpace(raw []byte, i int) int {
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
		i++
	}
	return i
}

func (d *Decoder) scanSkippedComment(start int) (int, error) {
	return d.scanCommentEnd(start)
}

func (d *Decoder) scanSkippedValue(start int, push func(byte, int) error) (int, error) {
	switch d.buf[start] {
	case '[':
		if err := push(containerArray, start); err != nil {
			return 0, err
		}
		return start + 1, nil
	case '{':
		if err := push(containerInline, start); err != nil {
			return 0, err
		}
		return start + 1, nil
	case '"', '\'':
		end, _, err := d.scanString(start)
		if err != nil {
			return 0, err
		}
		if len(d.buf[start:end]) > d.limits.MaxStringLength {
			err := &LimitError{Limit: "MaxStringLength", Value: d.limits.MaxStringLength, Span: [2]int{start, end}}
			d.setErr(err)
			return 0, err
		}
		return end, nil
	default:
		end := scanBareValueEnd(d.buf[start:])
		if end == 0 {
			return 0, d.syntaxError("invalid value", start)
		}
		raw := d.buf[start : start+end]
		if _, _, msg := classifyBareValue(raw); msg != "" {
			return 0, d.syntaxError(msg, start)
		}
		return start + end, nil
	}
}

func (d *Decoder) scanSkippedInlineKey(start int) (int, error) {
	i := start
	for i < len(d.buf) {
		switch d.buf[i] {
		case '=':
			key := bytesTrimRightSpaces(d.buf[start:i])
			if len(key) == 0 {
				return 0, d.syntaxError("empty key", start)
			}
			if !isSimpleBareKey(key) {
				if _, err := parseDottedKey(key); err != nil {
					d.setErr(err)
					return 0, err
				}
			}
			if len(key) > d.limits.MaxKeyLength {
				err := &LimitError{Limit: "MaxKeyLength", Value: d.limits.MaxKeyLength, Span: [2]int{start, i}}
				d.setErr(err)
				return 0, err
			}
			return i + 1, nil
		case '"', '\'':
			next, err := d.scanQuoted(d.buf[i], i)
			if err != nil {
				return 0, err
			}
			i = next
		case ',', '}', '\n', '\r', '#':
			return 0, d.syntaxError("expected equals", i)
		default:
			i++
		}
	}
	return 0, d.syntaxError("expected equals", i)
}

func skipRawValueToken(dec *Decoder, tok rawToken) error {
	depth := 1
	switch tok.Kind {
	case TokenKindArrayStart, TokenKindInlineTableStart:
	case TokenKindValueString, TokenKindValueInteger, TokenKindValueFloat, TokenKindValueBool, TokenKindValueDatetime:
		return nil
	default:
		return decoderSyntaxErrorForRawToken(dec, tok, "expected value")
	}
	for depth > 0 {
		next, err := dec.readToken()
		if err != nil {
			return err
		}
		switch next.Kind {
		case TokenKindArrayStart, TokenKindInlineTableStart:
			depth++
		case TokenKindArrayEnd, TokenKindInlineTableEnd:
			depth--
		}
	}
	return nil
}

func parseNextValue(dec *Decoder) (any, error) {
	for {
		tok, err := dec.readToken()
		if err != nil {
			return nil, err
		}
		if tok.Kind == TokenKindComment {
			continue
		}
		return parseRawValueToken(dec, tok)
	}
}

func parseValueToken(dec *Decoder, tok Token) (any, error) {
	return parseRawValueToken(dec, rawTokenFromToken(tok))
}

func parseRawValueToken(dec *Decoder, tok rawToken) (any, error) {
	switch tok.Kind {
	case TokenKindValueString:
		return parseStringValue(tok.Bytes)
	case TokenKindValueInteger:
		return rawTokenIntegerValue(dec, tok)
	case TokenKindValueFloat:
		return rawTokenFloatValue(dec, tok)
	case TokenKindValueBool:
		switch {
		case bytes.Equal(tok.Bytes, trueLiteral):
			return true, nil
		case bytes.Equal(tok.Bytes, falseLiteral):
			return false, nil
		default:
			return nil, decoderSyntaxErrorForRawToken(dec, tok, "malformed boolean")
		}
	case TokenKindValueDatetime:
		v, _, err := parseDateTimeValue(tok.Bytes)
		return v, err
	case TokenKindArrayStart:
		return parseArrayValue(dec)
	case TokenKindInlineTableStart:
		return parseInlineTableValue(dec)
	default:
		return nil, decoderSyntaxErrorForRawToken(dec, tok, "expected value")
	}
}

func rawTokenIntegerValue(dec *Decoder, tok rawToken) (int64, error) {
	if dec != nil {
		return scalarIntegerValue(dec.tokenScalar, tok.Bytes)
	}
	return parseIntegerLiteral(tok.Bytes)
}

func scalarIntegerValue(scalar tokenScalar, raw []byte) (int64, error) {
	if scalar.kind == tokenScalarInteger {
		return int64(scalar.bits), nil //nolint:gosec // G115: bits hold an int64 stored via uint64(i); the round-trip is exact.
	}
	return parseIntegerLiteral(raw)
}

func rawTokenFloatValue(dec *Decoder, tok rawToken) (float64, error) {
	scalar := tokenScalar{}
	if dec != nil {
		scalar = dec.tokenScalar
	}
	return scalarFloatValue(scalar, tok.Bytes, func() error {
		return decoderSyntaxErrorForRawToken(dec, tok, "malformed special float")
	})
}

func scalarFloatValue(scalar tokenScalar, raw []byte, malformedSpecial func() error) (float64, error) {
	if scalar.kind == tokenScalarFloat {
		return math.Float64frombits(scalar.bits), nil
	}
	if f, ok := parseSpecialFloatLiteral(raw); ok {
		return f, nil
	}
	if isSpecialFloatFolded(raw) {
		return 0, malformedSpecial()
	}
	return parseFloatLiteral(raw)
}

func parseArrayValue(dec *Decoder) ([]any, error) {
	values := make([]any, 0, documentMapHint)
	for {
		tok, err := dec.readToken()
		if err != nil {
			return nil, err
		}
		if tok.Kind == TokenKindComment {
			continue
		}
		switch tok.Kind {
		case TokenKindArrayEnd:
			return values, nil
		default:
			v, err := parseRawValueToken(dec, tok)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
	}
}

func parseInlineTableValue(dec *Decoder) (documentMap, error) {
	m := newDocumentMap()
	data := decoderSource(dec)
	closedInlineTables := make(map[string]string)
	arrayTableEpochs := map[string]int(nil)
	for {
		tok, err := dec.readToken()
		if err != nil {
			return nil, err
		}
		if tok.Kind == TokenKindComment {
			continue
		}
		switch tok.Kind {
		case TokenKindInlineTableEnd:
			return m, nil
		case TokenKindKey:
			key, err := parseDottedKey(tok.Bytes)
			if err != nil {
				return nil, err
			}
			if closedInlinePrefix(key, closedInlineTables, arrayTableEpochs) {
				return nil, semanticSyntaxErrorRaw(data, tok, "cannot extend inline table")
			}
			v, err := parseNextValue(dec)
			if err != nil {
				return nil, err
			}
			if err := assignUniqueRaw(data, m, key, v, tok); err != nil {
				return nil, err
			}
			closedInlineTables = markClosedValuePaths(closedInlineTables, key, v, arrayTableEpochs)
		default:
			return nil, decoderSyntaxErrorForRawToken(dec, tok, "expected inline table key")
		}
	}
}

type stringValueKind uint8

const (
	stringValueMalformed stringValueKind = iota
	stringValueLiteral
	stringValueMultilineLiteral
	stringValueBasic
	stringValueMultilineBasic
)

func parseStringValue(raw []byte) (string, error) {
	body, kind, err := stringValueBody(raw)
	if err != nil {
		return "", err
	}
	switch kind {
	case stringValueLiteral:
		if err := validateLiteralStringBody(body, false); err != nil {
			return "", err
		}
		return string(body), nil
	case stringValueMultilineLiteral:
		if err := validateLiteralStringBody(body, true); err != nil {
			return "", err
		}
		return string(body), nil
	case stringValueBasic:
		return parseBasicStringBody(body, false)
	case stringValueMultilineBasic:
		return parseBasicStringBody(body, true)
	default:
		return "", malformedStringError(raw)
	}
}

func validateStringValue(raw []byte) error {
	body, kind, err := stringValueBody(raw)
	if err != nil {
		return err
	}
	switch kind {
	case stringValueLiteral:
		return validateLiteralStringBody(body, false)
	case stringValueMultilineLiteral:
		return validateLiteralStringBody(body, true)
	case stringValueBasic:
		return validateBasicStringBody(body, false)
	case stringValueMultilineBasic:
		return validateBasicStringBody(body, true)
	default:
		return malformedStringError(raw)
	}
}

func stringValueBody(raw []byte) ([]byte, stringValueKind, error) {
	if hasTripleDelimiter(raw, '\'') {
		return trimInitialMultilineStringNewline(raw[3 : len(raw)-3]), stringValueMultilineLiteral, nil
	}
	if hasTripleDelimiter(raw, '"') {
		return trimInitialMultilineStringNewline(raw[3 : len(raw)-3]), stringValueMultilineBasic, nil
	}
	if hasPairDelimiter(raw, '\'') {
		return raw[1 : len(raw)-1], stringValueLiteral, nil
	}
	if hasPairDelimiter(raw, '"') {
		return raw[1 : len(raw)-1], stringValueBasic, nil
	}
	return nil, stringValueMalformed, malformedStringError(raw)
}

// hasTripleDelimiter reports whether raw opens and closes with three q bytes.
func hasTripleDelimiter(raw []byte, q byte) bool {
	return len(raw) >= 6 &&
		raw[0] == q && raw[1] == q && raw[2] == q &&
		raw[len(raw)-1] == q && raw[len(raw)-2] == q && raw[len(raw)-3] == q
}

// hasPairDelimiter reports whether raw opens and closes with a single q byte.
func hasPairDelimiter(raw []byte, q byte) bool {
	return len(raw) >= 2 && raw[0] == q && raw[len(raw)-1] == q
}

func malformedStringError(raw []byte) error {
	return &SyntaxError{Line: 1, Col: 1, Msg: "malformed string", Span: [2]int{0, len(raw)}}
}

func trimInitialMultilineStringNewline(raw []byte) []byte {
	if len(raw) >= 2 && raw[0] == '\r' && raw[1] == '\n' {
		return raw[2:]
	}
	if len(raw) >= 1 && raw[0] == '\n' {
		return raw[1:]
	}
	return raw
}

func validateLiteralStringBody(raw []byte, multiline bool) error {
	return validateStringControls(raw, multiline)
}

func validateBasicStringBody(raw []byte, multiline bool) error {
	return scanBasicStringBody(raw, multiline, nil)
}

func parseBasicStringBody(raw []byte, multiline bool) (string, error) {
	if !multiline && scan.ScanBasicStringStrict(raw) == len(raw) {
		return string(raw), nil
	}
	if multiline && bytes.IndexByte(raw, '\\') < 0 {
		if err := validateStringControls(raw, true); err != nil {
			return "", err
		}
		return string(raw), nil
	}
	var b strings.Builder
	b.Grow(len(raw))
	if err := scanBasicStringBody(raw, multiline, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

//nolint:cyclop,funlen,gocognit,gocyclo // basic-string escape-processing state machine; cohesive.
func scanBasicStringBody(raw []byte, multiline bool, b *strings.Builder) error {
	for i := 0; i < len(raw); {
		if !multiline { //nolint:nestif // single-line vs multiline body handling; cohesive.
			n := scan.ScanBasicStringStrict(raw[i:])
			if n > 0 {
				if b != nil {
					b.Write(raw[i : i+n])
				}
				i += n
				if i >= len(raw) {
					return nil
				}
			}
			c := raw[i]
			if c != '\\' {
				if prohibitedStringControl(c, false) {
					return stringControlError(raw, i)
				}
				if b != nil {
					b.WriteByte(c)
				}
				i++
				continue
			}
		} else {
			c := raw[i]
			if c != '\\' {
				if c == '\r' && i+1 < len(raw) && raw[i+1] == '\n' {
					if b != nil {
						b.WriteByte('\r')
						b.WriteByte('\n')
					}
					i += 2
					continue
				}
				if prohibitedStringControl(c, true) {
					return stringControlError(raw, i)
				}
				if b != nil {
					b.WriteByte(c)
				}
				i++
				continue
			}
			if next, ok := skipEscapedMultilineStringWhitespace(raw, i+1); ok {
				i = next
				continue
			}
		}
		if i+1 >= len(raw) {
			return &SyntaxError{Line: 1, Col: i + 1, Msg: "unterminated string escape", Span: [2]int{i, len(raw)}}
		}
		next := raw[i+1]
		switch next {
		case 'b':
			if b != nil {
				b.WriteByte('\b')
			}
			i += 2
		case 't':
			if b != nil {
				b.WriteByte('\t')
			}
			i += 2
		case 'n':
			if b != nil {
				b.WriteByte('\n')
			}
			i += 2
		case 'f':
			if b != nil {
				b.WriteByte('\f')
			}
			i += 2
		case 'r':
			if b != nil {
				b.WriteByte('\r')
			}
			i += 2
		case 'e':
			if b != nil {
				b.WriteByte(0x1b)
			}
			i += 2
		case '"':
			if b != nil {
				b.WriteByte('"')
			}
			i += 2
		case '\\':
			if b != nil {
				b.WriteByte('\\')
			}
			i += 2
		case 'x':
			r, err := parseUnicodeEscape(raw, i+2, 2)
			if err != nil {
				return err
			}
			if b != nil {
				b.WriteRune(r)
			}
			i += 4
		case 'u', 'U':
			digits := 4
			if next == 'U' {
				digits = 8
			}
			r, err := parseUnicodeEscape(raw, i+2, digits)
			if err != nil {
				return err
			}
			if b != nil {
				b.WriteRune(r)
			}
			i += 2 + digits
		default:
			return &SyntaxError{Line: 1, Col: i + 1, Msg: "invalid string escape", Span: [2]int{i, min(i+2, len(raw))}}
		}
	}
	return nil
}

func validateStringControls(raw []byte, multiline bool) error {
	if idx := prohibitedStringControlIndex(raw, multiline); idx >= 0 {
		return stringControlError(raw, idx)
	}
	return nil
}

//nolint:cyclop // whitespace/newline elision after a line-ending backslash; cohesive.
func skipEscapedMultilineStringWhitespace(raw []byte, i int) (int, bool) {
	j := i
	for j < len(raw) && (raw[j] == ' ' || raw[j] == '\t') {
		j++
	}
	switch {
	case j < len(raw) && raw[j] == '\n':
		j++
	case j+1 < len(raw) && raw[j] == '\r' && raw[j+1] == '\n':
		j += 2
	default:
		return i, false
	}
	for j < len(raw) {
		switch raw[j] {
		case ' ', '\t', '\n':
			j++
		case '\r':
			if j+1 < len(raw) && raw[j+1] == '\n' {
				j += 2
				continue
			}
			return j, true
		default:
			return j, true
		}
	}
	return j, true
}

func parseUnicodeEscape(raw []byte, start, digits int) (rune, error) {
	end := start + digits
	if end > len(raw) {
		return 0, &SyntaxError{Line: 1, Col: start + 1, Msg: "short unicode escape", Span: [2]int{start - 2, len(raw)}}
	}
	var v int64
	for _, c := range raw[start:end] {
		n, ok := hexDigitValue(c)
		if !ok {
			return 0, &SyntaxError{Line: 1, Col: start + 1, Msg: "invalid unicode escape", Span: [2]int{start - 2, end}}
		}
		v = v<<4 | int64(n)
	}
	if v > maxParseInt32 {
		return 0, &strconv.NumError{Func: "ParseInt", Num: string(raw[start:end]), Err: strconv.ErrRange}
	}
	r := rune(v)
	if !utf8.ValidRune(r) {
		return 0, &SyntaxError{Line: 1, Col: start + 1, Msg: "invalid unicode scalar", Span: [2]int{start - 2, end}}
	}
	return r, nil
}

const maxParseInt32 = 1<<31 - 1

func hexDigitValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	default:
		return 0, false
	}
}

func prohibitedStringControlIndex(raw []byte, multiline bool) int {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if multiline && c == '\r' && i+1 < len(raw) && raw[i+1] == '\n' {
			i++
			continue
		}
		if prohibitedStringControl(c, multiline) {
			return i
		}
	}
	return -1
}

func prohibitedStringControl(c byte, multiline bool) bool {
	if c == '\t' {
		return false
	}
	if multiline && c == '\n' {
		return false
	}
	return c < 0x20 || c == 0x7f
}

func stringControlError(raw []byte, idx int) error {
	return &SyntaxError{
		Line: 1,
		Col:  idx + 1,
		Msg:  fmt.Sprintf("unescaped control character 0x%02x in string", raw[idx]),
		Span: [2]int{idx, idx + 1},
	}
}

func parseHeaderKey(raw []byte, array bool) ([]string, error) {
	return parseDottedKey(trimHeaderKey(raw, array))
}

func trimHeaderKey(raw []byte, array bool) []byte {
	raw = bytesTrimSpace(raw)
	if array {
		if len(raw) >= 4 {
			return bytesTrimSpace(raw[2 : len(raw)-2])
		}
		return nil
	}
	if len(raw) >= 2 {
		return bytesTrimSpace(raw[1 : len(raw)-1])
	}
	return nil
}

func isSimpleBareKey(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	for _, c := range raw {
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

//nolint:cyclop,gocognit // dotted-key parser with quoted/bare segments; cohesive.
func parseDottedKey(raw []byte) ([]string, error) {
	origLen := len(raw)
	raw = bytesTrimSpace(raw)
	parts := make([]string, 0, bytes.Count(raw, []byte("."))+1)
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
				return nil, &SyntaxError{Line: 1, Col: 1, Msg: "unterminated quoted key", Span: [2]int{0, origLen}}
			}
			v, err := parseStringValue(raw[:end+1])
			if err != nil {
				return nil, err
			}
			part = v
			raw = raw[end+1:]
		} else {
			end := bytes.IndexAny(raw, ". \t")
			if end < 0 {
				part = string(raw)
				raw = raw[:0]
			} else {
				part = string(raw[:end])
				raw = raw[end:]
			}
			if part == "" {
				return nil, &SyntaxError{Line: 1, Col: 1, Msg: "empty key segment", Span: [2]int{0, origLen}}
			}
			if !isSimpleBareKey([]byte(part)) {
				return nil, &SyntaxError{Line: 1, Col: 1, Msg: "unexpected token in dotted key", Span: [2]int{0, origLen}}
			}
		}
		parts = append(parts, part)
		raw = bytes.TrimLeft(raw, " \t")
		if len(raw) == 0 {
			break
		}
		if raw[0] != '.' {
			return nil, &SyntaxError{Line: 1, Col: 1, Msg: "unexpected token in dotted key", Span: [2]int{0, origLen}}
		}
		raw = raw[1:]
		if len(bytes.TrimLeft(raw, " \t")) == 0 {
			return nil, &SyntaxError{Line: 1, Col: 1, Msg: "empty key segment", Span: [2]int{0, origLen}}
		}
	}
	if len(parts) == 0 {
		return nil, &SyntaxError{Line: 1, Col: 1, Msg: "empty key", Span: [2]int{0, origLen}}
	}
	return parts, nil
}

func appendNormalizedNumeric(dst, raw []byte, lower bool) ([]byte, bool) {
	needsCopy := false
	for _, b := range raw {
		if b == '_' || (lower && 'A' <= b && b <= 'Z') {
			needsCopy = true
			break
		}
	}
	if !needsCopy {
		return raw, false
	}
	for _, b := range raw {
		if b == '_' {
			continue
		}
		if lower && 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		dst = append(dst, b)
	}
	return dst, true
}

func parseFloatLiteral(raw []byte) (float64, error) {
	var stack [128]byte
	textBytes, copied := appendNormalizedNumeric(stack[:0], raw, true)
	if !copied {
		return strconv.ParseFloat(unsafeString(raw), 64)
	}
	return strconv.ParseFloat(unsafeString(textBytes), 64)
}

func parseSpecialFloatLiteral(raw []byte) (float64, bool) {
	switch {
	case bytes.Equal(raw, nanLiteral), bytes.Equal(raw, posNanLiteral):
		return math.NaN(), true
	case bytes.Equal(raw, negNanLiteral):
		return math.Copysign(math.NaN(), -1), true
	case bytes.Equal(raw, infLiteral), bytes.Equal(raw, posInfLiteral):
		return math.Inf(1), true
	case bytes.Equal(raw, negInfLiteral):
		return math.Inf(-1), true
	default:
		return 0, false
	}
}

func isSpecialFloatFolded(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '+' || raw[0] == '-' {
		raw = raw[1:]
	}
	if len(raw) != 3 {
		return false
	}
	return lowerASCII(raw[0]) == 'i' && lowerASCII(raw[1]) == 'n' && lowerASCII(raw[2]) == 'f' ||
		lowerASCII(raw[0]) == 'n' && lowerASCII(raw[1]) == 'a' && lowerASCII(raw[2]) == 'n'
}

func parseIntegerLiteral(raw []byte) (int64, error) {
	text := raw
	sign := int64(1)
	if len(text) > 0 && text[0] == '+' {
		text = text[1:]
	} else if len(text) > 0 && text[0] == '-' {
		sign = -1
		text = text[1:]
	}
	base := byte(10)
	if len(text) > 2 && text[0] == '0' {
		switch text[1] {
		case 'b', 'B':
			base = 2
			text = text[2:]
		case 'o', 'O':
			base = 8
			text = text[2:]
		case 'x', 'X':
			base = 16
			text = text[2:]
		}
	}
	limit := uint64(math.MaxInt64)
	if sign < 0 {
		limit = 1 << 63
	}
	u, err := parseUintLiteralDigits(text, base, limit)
	if err != nil {
		return 0, err
	}
	if sign < 0 {
		const minMagnitude = uint64(1) << 63
		if u == minMagnitude {
			return math.MinInt64, nil
		}
		return -int64(u), nil //nolint:gosec // G115: u < 2^63 here (u==2^63 handled above), so -int64(u) is in range.
	}
	return int64(u), nil //nolint:gosec // G115: u <= MaxInt64 under the positive limit, so int64(u) is in range.
}

func parseUintLiteralDigits(raw []byte, base byte, limit uint64) (uint64, error) {
	if len(raw) == 0 {
		return 0, strconv.ErrSyntax
	}
	// Hoist the overflow cutoff out of the loop. u*base+d > limit holds iff
	// u > limit/base, or u == limit/base and d > limit%base. Computing
	// cutoff/cutlim once replaces the per-digit division (which the runtime
	// base blocked from strength-reducing) with two comparisons per digit.
	base64 := uint64(base)
	cutoff := limit / base64
	cutlim := limit % base64
	var u uint64
	sawDigit := false
	for _, c := range raw {
		if c == '_' {
			continue
		}
		digit, ok := numericDigitValue(c)
		if !ok || digit >= base {
			return 0, strconv.ErrSyntax
		}
		sawDigit = true
		d := uint64(digit)
		if u > cutoff || (u == cutoff && d > cutlim) {
			return 0, strconv.ErrRange
		}
		u = u*base64 + d
	}
	if !sawDigit {
		return 0, strconv.ErrSyntax
	}
	return u, nil
}

func numericDigitValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func keyPath(raw []byte) ([]string, error) {
	if isSimpleBareKey(raw) {
		return []string{string(raw)}, nil
	}
	return parseDottedKey(raw)
}

func declarationContext(path []string, arrayTableEpochs map[string]int) string {
	for i := len(path); i > 0; i-- {
		prefix := encodedKeyPath(path[:i])
		if epoch := arrayTableEpochs[prefix]; epoch > 0 {
			return prefix + "#" + strconv.Itoa(epoch)
		}
	}
	return ""
}

func closedInlinePrefix(path []string, closed map[string]string, arrayTableEpochs map[string]int) bool {
	for i := 1; i <= len(path); i++ {
		prefix := path[:i]
		if existingContext, ok := closed[encodedKeyPath(prefix)]; ok && existingContext == declarationContext(prefix, arrayTableEpochs) {
			return true
		}
	}
	return false
}

func markClosedValuePaths(closed map[string]string, path []string, value any, arrayTableEpochs map[string]int) map[string]string {
	key := encodedKeyPath(path)
	switch v := value.(type) {
	case documentMap:
		if closed == nil {
			closed = make(map[string]string, documentMapHint)
		}
		closed[key] = declarationContext(path, arrayTableEpochs)
		for childKey, child := range v {
			childPath := append(append([]string(nil), path...), childKey)
			closed = markClosedValuePaths(closed, childPath, child, arrayTableEpochs)
		}
	case []any:
		if closed == nil {
			closed = make(map[string]string, documentMapHint)
		}
		closed[key] = declarationContext(path, arrayTableEpochs)
		for _, child := range v {
			if childMap, ok := child.(documentMap); ok {
				for childKey, grandchild := range childMap {
					childPath := append(append([]string(nil), path...), childKey)
					closed = markClosedValuePaths(closed, childPath, grandchild, arrayTableEpochs)
				}
			}
		}
	}
	return closed
}

func markDottedKeyTables(dotted map[string]string, fullPath []string, currentPathLen int, arrayTableEpochs map[string]int) map[string]string {
	for i := currentPathLen + 1; i < len(fullPath); i++ {
		if dotted == nil {
			dotted = make(map[string]string, documentMapHint)
		}
		prefix := fullPath[:i]
		dotted[encodedKeyPath(prefix)] = declarationContext(prefix, arrayTableEpochs)
	}
	return dotted
}

func encodedKeyPath(path []string) string {
	switch len(path) {
	case 0:
		return ""
	case 1:
		return path[0]
	default:
		return strings.Join(path, "\x00")
	}
}

func semanticSyntaxErrorRaw(data []byte, tok rawToken, msg string) *SyntaxError {
	return syntaxErrorForRawToken(data, tok, msg)
}

func ensureTableRaw(data []byte, root documentMap, path []string, tok rawToken) (documentMap, error) {
	cur := root
	for _, p := range path {
		next, _ := cur[p].(documentMap)
		if next == nil {
			if arr, ok := cur[p].([]any); ok && len(arr) > 0 {
				if last, ok := arr[len(arr)-1].(documentMap); ok {
					next = last
				}
			}
		}
		if next == nil {
			if _, exists := cur[p]; exists {
				return nil, semanticSyntaxErrorRaw(data, tok, "cannot redefine value as table")
			}
			next = newDocumentMap()
			cur[p] = next
		}
		cur = next
	}
	return cur, nil
}

func appendArrayTableRaw(data []byte, root documentMap, path []string, tok rawToken) (documentMap, error) {
	if len(path) == 0 {
		return root, nil
	}
	parent, err := ensureTableRaw(data, root, path[:len(path)-1], tok)
	if err != nil {
		return nil, err
	}
	name := path[len(path)-1]
	table := newDocumentMap()
	existing, exists := parent[name]
	arr, _ := existing.([]any)
	if exists && arr == nil {
		return nil, semanticSyntaxErrorRaw(data, tok, "cannot redefine value as array table")
	}
	arr = append(arr, table)
	parent[name] = arr
	return table, nil
}

func appendArrayTableKeyRaw(data []byte, root documentMap, name string, capacityHint int, tok rawToken) (documentMap, error) {
	table := newDocumentMap()
	arr, _ := root[name].([]any)
	if arr == nil {
		if _, exists := root[name]; exists {
			return nil, semanticSyntaxErrorRaw(data, tok, "cannot redefine value as array table")
		}
	}
	if arr == nil && capacityHint > 0 {
		arr = make([]any, 0, capacityHint)
	}
	arr = append(arr, table)
	root[name] = arr
	return table, nil
}

func assignUniqueRaw(data []byte, root documentMap, path []string, value any, tok rawToken) error {
	cur := root
	for _, p := range path[:len(path)-1] {
		next, _ := cur[p].(documentMap)
		if next == nil {
			if _, exists := cur[p]; exists {
				return semanticSyntaxErrorRaw(data, tok, "cannot redefine value as table")
			}
			next = newDocumentMap()
			cur[p] = next
		}
		cur = next
	}
	key := path[len(path)-1]
	if _, exists := cur[key]; exists {
		return semanticSyntaxErrorRaw(data, tok, "duplicate key")
	}
	cur[key] = value
	return nil
}

func newDocumentMap() documentMap {
	m := documentMapPool.Get().(documentMap)
	clear(m)
	return m
}

func recycleDocument(v any) {
	switch x := v.(type) {
	case documentMap:
		for _, child := range x {
			recycleDocument(child)
		}
		clear(x)
		documentMapPool.Put(x)
	case []any:
		for _, child := range x {
			recycleDocument(child)
		}
	}
}

func bytesTrimSpace(raw []byte) []byte {
	for len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t' || raw[0] == '\n' || raw[0] == '\r') {
		raw = raw[1:]
	}
	for len(raw) > 0 {
		c := raw[len(raw)-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		raw = raw[:len(raw)-1]
	}
	return raw
}
