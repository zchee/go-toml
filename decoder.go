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
	"io"
	"math"

	"github.com/zchee/go-toml/internal/scan"
)

const (
	// DefaultMaxNestedDepth is the container nesting cap.
	DefaultMaxNestedDepth = 1024
	// DefaultMaxKeyLength is the default maximum key length in bytes.
	DefaultMaxKeyLength = 64 * 1024
	// DefaultMaxStringLength is the default maximum string length in bytes.
	DefaultMaxStringLength = 16 << 20
	// DefaultMaxDocumentSize is the default maximum reader input size.
	DefaultMaxDocumentSize = 16 << 20
	// MaxKeyLength is the default key-length cap used by Decoder.
	MaxKeyLength = DefaultMaxKeyLength
)

// Option modifies decoder construction behavior.
type Option func(*Decoder)

// Limits are the parser DoS caps for a Decoder.
type Limits struct {
	MaxNestedDepth  int
	MaxKeyLength    int
	MaxStringLength int
	MaxDocumentSize int
}

// WithLimits configures DoS caps for the returned Decoder.
func WithLimits(l Limits) Option {
	return func(d *Decoder) {
		if l.MaxNestedDepth > 0 {
			d.limits.MaxNestedDepth = l.MaxNestedDepth
		}
		if l.MaxKeyLength > 0 {
			d.limits.MaxKeyLength = l.MaxKeyLength
		}
		if l.MaxStringLength > 0 {
			d.limits.MaxStringLength = l.MaxStringLength
		}
		if l.MaxDocumentSize > 0 {
			d.limits.MaxDocumentSize = l.MaxDocumentSize
		}
	}
}

// WithLocalAsUTC allows local TOML datetime forms to decode into time.Time as UTC.
func WithLocalAsUTC() Option {
	return func(d *Decoder) {
		d.localAsUTC = true
	}
}

// WithCopiedStrings forces decoded string values to use independent string
// allocations instead of aliasing the input buffer.
//
// By default the direct decode path aliases the input buffer for escape-free
// string values (zero copy), so those strings are valid only while the input
// is unmodified. WithCopiedStrings opts out of that aliasing: every decoded
// string is copied, so it stays valid regardless of later input mutation.
func WithCopiedStrings() Option {
	return func(d *Decoder) {
		d.copyStrings = true
	}
}

func withoutTokenPositions(d *Decoder) {
	d.trackTokenPositions = false
}

var internalDecoderOptionsNoTokenPositions = []Option{withoutTokenPositions}

func decoderOptionsWithoutTokenPositions(opts []Option) []Option {
	if len(opts) == 0 {
		return internalDecoderOptionsNoTokenPositions
	}
	out := make([]Option, 0, len(opts)+1)
	out = append(out, opts...)
	out = append(out, withoutTokenPositions)
	return out
}

// Decoder reads bytes as a token stream.
//
// `Token.Bytes` aliases source bytes for NewDecoderBytes and aliases an internal
// mutable buffer for NewDecoder. Aliasing is valid until the next ReadToken call.
// Copy if callers need persistence across the next token.
//
// BOM handling: a UTF-8 BOM at the first token position is skipped.
type Decoder struct {
	buf            []byte
	off            int
	decodeStart    int
	line           int
	col            int
	err            error
	decoded        bool
	expectingValue bool
	valueNoNewline bool
	needSeparator  bool
	needLineEnd    bool
	atLineStart    bool

	limits         Limits
	arrayDepth     int
	inlineDepth    int
	containerStack []byte
	localAsUTC     bool
	copyStrings    bool
	tokenLine      int
	tokenCol       int
	tokenScalar    tokenScalar

	trackTokenPositions bool
}

// containerArray and containerInline mark entries pushed onto containerStack
// to distinguish array context (where `,` introduces a value) from inline
// table context (where `,` introduces a key).
const (
	containerArray  byte = 'a'
	containerInline byte = 'i'
)

// innermostIsArray reports whether the innermost open container is an array.
// Top-level (no open container) returns false.
func (d *Decoder) innermostIsArray() bool {
	n := len(d.containerStack)
	if n == 0 {
		return false
	}
	return d.containerStack[n-1] == containerArray
}

// NewDecoder creates a Decoder over an io.Reader input.
func NewDecoder(r io.Reader, opts ...Option) *Decoder {
	b, err := io.ReadAll(r)
	d := &Decoder{
		off:                 0,
		line:                1,
		col:                 1,
		atLineStart:         true,
		trackTokenPositions: true,
		limits: Limits{
			MaxNestedDepth:  DefaultMaxNestedDepth,
			MaxKeyLength:    DefaultMaxKeyLength,
			MaxStringLength: DefaultMaxStringLength,
			MaxDocumentSize: DefaultMaxDocumentSize,
		},
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.err != nil {
		return d
	}

	d.buf = b
	if err != nil {
		d.err = err
		return d
	}
	if d.limits.MaxDocumentSize > 0 && len(d.buf) > d.limits.MaxDocumentSize {
		d.err = &LimitError{Limit: "MaxDocumentSize", Value: d.limits.MaxDocumentSize, Span: [2]int{0, d.limits.MaxDocumentSize}}
		return d
	}

	if bad := scan.ValidateUTF8(d.buf); bad != len(d.buf) {
		d.err = &SyntaxError{Line: 1, Col: bad + 1, Msg: "invalid utf-8", Span: [2]int{bad, bad + 1}}
		return d
	}
	d.skipBOM()
	d.decodeStart = d.off
	return d
}

// NewDecoderBytes creates a Decoder over an in-memory TOML payload.
func NewDecoderBytes(data []byte, opts ...Option) *Decoder {
	d := &Decoder{
		off:                 0,
		line:                1,
		col:                 1,
		atLineStart:         true,
		buf:                 data,
		trackTokenPositions: true,
		limits: Limits{
			MaxNestedDepth:  DefaultMaxNestedDepth,
			MaxKeyLength:    DefaultMaxKeyLength,
			MaxStringLength: DefaultMaxStringLength,
			MaxDocumentSize: DefaultMaxDocumentSize,
		},
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.limits.MaxDocumentSize > 0 && len(d.buf) > d.limits.MaxDocumentSize {
		d.err = &LimitError{Limit: "MaxDocumentSize", Value: d.limits.MaxDocumentSize, Span: [2]int{0, d.limits.MaxDocumentSize}}
		return d
	}
	if bad := scan.ValidateUTF8(d.buf); bad != len(d.buf) {
		d.err = &SyntaxError{Line: 1, Col: bad + 1, Msg: "invalid utf-8", Span: [2]int{bad, bad + 1}}
		return d
	}
	d.skipBOM()
	d.decodeStart = d.off
	return d
}

// Decode decodes the decoder's TOML document into dst.
//
// Decode is a whole-document convenience over Unmarshal for callers that build a
// decoder from an io.Reader. It is only valid before any ReadToken call consumes
// the token stream; mixed token reads and value binding on the same decoder are
// rejected with *DecoderStateError. Decode is not a multi-document streaming API.
func (d *Decoder) Decode(dst any) error {
	if d == nil {
		return io.ErrUnexpectedEOF
	}
	if d.err != nil {
		return d.err
	}
	if d.decoded || d.off != d.decodeStart || d.expectingValue || d.arrayDepth != 0 || d.inlineDepth != 0 || len(d.containerStack) != 0 {
		return &DecoderStateError{Offset: d.off}
	}
	if err := unmarshalWithOptions(d.buf, dst, UnmarshalOptions{DecoderOptions: d.decodeOptions()}); err != nil {
		return err
	}
	d.decoded = true
	d.off = len(d.buf)
	return nil
}

func (d *Decoder) decodeOptions() []Option {
	opts := []Option{WithLimits(d.limits)}
	if d.localAsUTC {
		opts = append(opts, WithLocalAsUTC())
	}
	if d.copyStrings {
		opts = append(opts, WithCopiedStrings())
	}
	return opts
}

// ReadToken returns the next token in the stream.
//
// The return contract is fail-fast: the first SyntaxError or LimitError enters
// sticky state and is returned until the decoder is replaced.
func (d *Decoder) ReadToken() (Token, error) {
	tok, err := d.readToken()
	if err != nil {
		return Token{}, err
	}
	pub := tok.publicToken()
	pub.scalar = d.tokenScalar
	if d.trackTokenPositions {
		pub.Line = d.tokenLine
		pub.Col = d.tokenCol
	}
	return pub, nil
}

//nolint:cyclop,funlen,gocognit,gocyclo // central token-stream dispatch state machine; cohesive.
func (d *Decoder) readToken() (rawToken, error) {
	if d == nil {
		return rawToken{}, io.ErrUnexpectedEOF
	}
	if d.err != nil {
		return rawToken{}, d.err
	}

	for {
		if d.off >= len(d.buf) {
			if d.expectingValue {
				return rawToken{}, d.syntaxError("expected value", d.off)
			}
			return rawToken{}, io.EOF
		}

		d.skipSpaces()
		if d.off >= len(d.buf) {
			if d.expectingValue {
				return rawToken{}, d.syntaxError("expected value", d.off)
			}
			return rawToken{}, io.EOF
		}

		if d.needLineEnd {
			switch d.buf[d.off] {
			case '#':
				return d.scanComment()
			case '\n', '\r':
				continue
			default:
				return rawToken{}, d.syntaxError("expected newline", d.off)
			}
		}

		if d.atLineStart && !d.expectingValue {
			if d.matchPrefix("[[") {
				return d.scanArrayTableHeader()
			}
			if d.buf[d.off] == '[' {
				return d.scanTableHeader()
			}
		}

		ch := d.buf[d.off]
		switch ch {
		case '[':
			if d.innermostIsArray() && !d.expectingValue {
				return rawToken{}, d.syntaxError("expected comma", d.off)
			}
			return d.scanArrayStart()
		case '{':
			if d.innermostIsArray() && !d.expectingValue {
				return rawToken{}, d.syntaxError("expected comma", d.off)
			}
			return d.scanInlineTableStart()
		case ']':
			return d.scanArrayEnd()
		case '}':
			return d.scanInlineTableEnd()
		case '#':
			return d.scanComment()
		case '=':
			return rawToken{}, d.syntaxError("unexpected equals", d.off)
		case ',':
			if !d.needSeparator {
				return rawToken{}, d.syntaxError("unexpected comma", d.off)
			}
			d.advanceOne()
			d.needSeparator = false
			if d.innermostIsArray() {
				d.expectingValue = true
			} else {
				d.expectingValue = false
			}
			continue
		default:
			if d.expectingValue {
				if d.needSeparator && d.innermostIsArray() {
					return rawToken{}, d.syntaxError("expected array separator", d.off)
				}
				return d.scanValueToken()
			}
			if d.needSeparator && len(d.containerStack) > 0 {
				return rawToken{}, d.syntaxError("expected inline table separator", d.off)
			}
			return d.scanKeyToken()
		}
	}
}

func (d *Decoder) skipSpaces() {
	for d.off < len(d.buf) {
		rem := d.buf[d.off:]
		n := scan.SkipWhitespace(rem)
		if n > 0 {
			wasAtLineStart := d.atLineStart
			d.advanceBytes(rem[:n])
			if wasAtLineStart {
				// still at the beginning of line until first non-space.
				d.atLineStart = true
			}
			continue
		}

		if len(rem) > 0 && rem[0] == '\r' {
			if len(rem) >= 2 && rem[1] == '\n' {
				if d.valueNoNewline {
					return
				}
				d.advanceBytes(rem[:2])
				d.atLineStart = true
				d.needLineEnd = false
				continue
			}
		}
		if len(rem) > 0 && rem[0] == '\n' {
			if d.valueNoNewline {
				return
			}
			d.advanceBytes(rem[:1])
			d.atLineStart = true
			d.needLineEnd = false
			continue
		}
		if len(rem) > 0 && rem[0] == '#' {
			return
		}
		return
	}
}

func (d *Decoder) scanComment() (rawToken, error) {
	start := d.off
	end, err := d.scanCommentEnd(start)
	if err != nil {
		return rawToken{}, err
	}
	tok := d.makeToken(TokenKindComment, d.buf[start:end], start, tokenScalar{})
	d.advanceBytes(d.buf[start:end])
	return tok, nil
}

func (d *Decoder) scanCommentEnd(start int) (int, error) {
	bodyStart := start + 1
	if bodyStart > len(d.buf) {
		return len(d.buf), nil
	}
	end := bodyStart + scan.ScanCommentBody(d.buf[bodyStart:])
	if end >= len(d.buf) {
		return end, nil
	}
	switch d.buf[end] {
	case '\n':
		return end, nil
	case '\r':
		if end+1 < len(d.buf) && d.buf[end+1] == '\n' {
			return end, nil
		}
	}
	return 0, d.syntaxError("control character in comment", end)
}

//nolint:cyclop // bare/quoted/dotted key scan with inline limit checks; cohesive.
func (d *Decoder) scanKeyToken() (rawToken, error) {
	start := d.off
	i := start
	for i < len(d.buf) {
		ch := d.buf[i]
		if ch == '=' {
			break
		}
		if ch == '#' || ch == '\n' || ch == '\r' {
			break
		}
		if ch == '"' || ch == '\'' {
			j, err := d.scanQuoted(ch, i)
			if err != nil {
				return rawToken{}, err
			}
			i = j
			continue
		}
		i++
	}
	if i == start {
		return rawToken{}, d.syntaxError("empty key", i)
	}

	key := bytesTrimRightSpaces(d.buf[start:i])
	if !isSimpleBareKey(key) {
		if _, err := parseDottedKey(key); err != nil {
			d.setErr(err)
			return rawToken{}, err
		}
	}
	if len(key) > d.limits.MaxKeyLength {
		err := &LimitError{Limit: "MaxKeyLength", Value: d.limits.MaxKeyLength, Span: [2]int{start, i}}
		d.setErr(err)
		return rawToken{}, err
	}
	eq := i
	for eq < len(d.buf) && (d.buf[eq] == ' ' || d.buf[eq] == '\t') {
		eq++
	}
	if eq >= len(d.buf) || d.buf[eq] != '=' {
		return rawToken{}, d.syntaxError("expected equals", eq)
	}
	tok := d.makeToken(TokenKindKey, key, start, tokenScalar{})
	d.advanceBytes(d.buf[start : eq+1])
	d.expectingValue = true
	d.valueNoNewline = true
	d.needLineEnd = false
	return tok, nil
}

func (d *Decoder) scanValueToken() (rawToken, error) {
	start := d.off

	ch := d.buf[start]
	switch ch {
	case '"', '\'':
		end, kind, err := d.scanString(start)
		if err != nil {
			return rawToken{}, err
		}
		chunk := d.buf[start:end]
		if len(chunk) > d.limits.MaxStringLength {
			err := &LimitError{Limit: "MaxStringLength", Value: d.limits.MaxStringLength, Span: [2]int{start, end}}
			d.setErr(err)
			return rawToken{}, err
		}
		tok := d.makeToken(kind, chunk, start, tokenScalar{})
		d.advanceBytes(chunk)
		d.expectingValue = false
		d.valueNoNewline = false
		if d.innermostIsArray() {
			d.expectingValue = true
		}
		d.needSeparator = len(d.containerStack) > 0
		d.needLineEnd = len(d.containerStack) == 0
		return tok, nil
	case '[':
		return d.scanArrayStart()
	case '{':
		return d.scanInlineTableStart()
	case ']':
		return d.scanArrayEnd()
	case '}':
		return d.scanInlineTableEnd()
	case '#':
		return d.scanComment()
	}

	end := scanBareValueEnd(d.buf[start:])
	if end == 0 {
		return rawToken{}, d.syntaxError("invalid value", start)
	}
	end += start
	chunk := d.buf[start:end]
	if len(chunk) == 0 {
		return rawToken{}, d.syntaxError("expected value", start)
	}
	kind, scalar, msg := classifyBareValue(chunk)
	if msg != "" {
		return rawToken{}, d.syntaxError(msg, start)
	}
	tok := d.makeToken(kind, chunk, start, scalar)
	d.advanceBytes(chunk)
	d.expectingValue = false
	d.valueNoNewline = false
	if d.innermostIsArray() {
		d.expectingValue = true
	}
	d.needSeparator = len(d.containerStack) > 0
	d.needLineEnd = len(d.containerStack) == 0
	return tok, nil
}

func (d *Decoder) scanArrayStart() (rawToken, error) {
	start := d.off
	tok := d.makeToken(TokenKindArrayStart, d.buf[start:start+1], start, tokenScalar{})
	d.advanceOne()
	d.arrayDepth++
	d.enforceNestedDepth(start, d.arrayDepth)
	d.containerStack = append(d.containerStack, containerArray)
	d.expectingValue = true
	d.valueNoNewline = false
	d.needSeparator = false
	return tok, nil
}

func (d *Decoder) scanArrayEnd() (rawToken, error) {
	start := d.off
	tok := d.makeToken(TokenKindArrayEnd, d.buf[start:start+1], start, tokenScalar{})
	d.advanceOne()
	if d.arrayDepth > 0 {
		d.arrayDepth--
	}
	if n := len(d.containerStack); n > 0 && d.containerStack[n-1] == containerArray {
		d.containerStack = d.containerStack[:n-1]
	}
	d.expectingValue = d.innermostIsArray()
	d.valueNoNewline = false
	d.needSeparator = len(d.containerStack) > 0
	d.needLineEnd = len(d.containerStack) == 0
	return tok, nil
}

func (d *Decoder) scanInlineTableStart() (rawToken, error) {
	start := d.off
	tok := d.makeToken(TokenKindInlineTableStart, d.buf[start:start+1], start, tokenScalar{})
	d.advanceOne()
	d.inlineDepth++
	d.containerStack = append(d.containerStack, containerInline)
	d.expectingValue = false
	d.valueNoNewline = false
	d.needSeparator = false
	return tok, nil
}

func (d *Decoder) scanInlineTableEnd() (rawToken, error) {
	start := d.off
	tok := d.makeToken(TokenKindInlineTableEnd, d.buf[start:start+1], start, tokenScalar{})
	d.advanceOne()
	if d.inlineDepth > 0 {
		d.inlineDepth--
	}
	if n := len(d.containerStack); n > 0 && d.containerStack[n-1] == containerInline {
		d.containerStack = d.containerStack[:n-1]
	}
	d.expectingValue = d.innermostIsArray()
	d.valueNoNewline = false
	d.needSeparator = len(d.containerStack) > 0
	d.needLineEnd = len(d.containerStack) == 0
	return tok, nil
}

func (d *Decoder) scanTableHeader() (rawToken, error) {
	start := d.off
	i, err := d.scanHeaderEnd(start+1, false)
	if err != nil {
		return rawToken{}, err
	}
	if err := d.validateHeaderKey(start+1, i); err != nil {
		return rawToken{}, err
	}
	tokenEnd := i + 1
	head := d.buf[start:tokenEnd]
	tok := d.makeToken(TokenKindTableHeader, head, start, tokenScalar{})
	d.advanceBytes(head)
	keyLen := max(tokenEnd-start-2, 0)
	if keyLen > d.limits.MaxKeyLength {
		err := &LimitError{Limit: "MaxKeyLength", Value: d.limits.MaxKeyLength, Span: [2]int{start + 1, i}}
		d.setErr(err)
		return rawToken{}, err
	}
	d.expectingValue = false
	d.atLineStart = false
	d.needLineEnd = true
	return tok, nil
}

func (d *Decoder) scanArrayTableHeader() (rawToken, error) {
	start := d.off
	i, err := d.scanHeaderEnd(start+2, true)
	if err != nil {
		return rawToken{}, err
	}
	if err := d.validateHeaderKey(start+2, i); err != nil {
		return rawToken{}, err
	}
	tokenEnd := i + 2
	head := d.buf[start:tokenEnd]
	tok := d.makeToken(TokenKindArrayTableHeader, head, start, tokenScalar{})
	d.advanceBytes(head)
	keyLen := max(tokenEnd-start-4, 0)
	if keyLen > d.limits.MaxKeyLength {
		err := &LimitError{Limit: "MaxKeyLength", Value: d.limits.MaxKeyLength, Span: [2]int{start + 2, i}}
		d.setErr(err)
		return rawToken{}, err
	}
	d.expectingValue = false
	d.atLineStart = false
	d.needLineEnd = true
	return tok, nil
}

func (d *Decoder) scanHeaderEnd(i int, array bool) (int, error) {
	for i < len(d.buf) {
		ch := d.buf[i]
		if ch == '\n' || ch == '\r' {
			if array {
				return 0, d.syntaxError("unterminated array-of-tables header", i)
			}
			return 0, d.syntaxError("unterminated table header", i)
		}
		if ch == '"' || ch == '\'' {
			j, err := d.scanQuoted(ch, i)
			if err != nil {
				return 0, err
			}
			i = j
			continue
		}
		if ch == ']' {
			if !array {
				return i, nil
			}
			if i+1 < len(d.buf) && d.buf[i+1] == ']' {
				return i, nil
			}
		}
		i++
	}
	if array {
		return 0, d.syntaxError("unterminated array-of-tables header", len(d.buf))
	}
	return 0, d.syntaxError("unterminated table header", len(d.buf))
}

func (d *Decoder) validateHeaderKey(start, end int) error {
	i := start
	needSegment := true
	for {
		for i < end && (d.buf[i] == ' ' || d.buf[i] == '\t') {
			i++
		}
		if i >= end {
			if needSegment {
				return d.syntaxError("empty table key", end)
			}
			return nil
		}
		if !needSegment {
			if d.buf[i] != '.' {
				return d.syntaxError("unexpected token in table key", i)
			}
			i++
			needSegment = true
			continue
		}
		if d.buf[i] == '"' || d.buf[i] == '\'' { //nolint:nestif // quoted vs bare table-key segment branch; cohesive.
			j, err := d.scanQuoted(d.buf[i], i)
			if err != nil {
				return err
			}
			if j > end {
				return d.syntaxError("unterminated quoted table key", i)
			}
			i = j
		} else {
			n := scan.ScanBareKey(d.buf[i:end])
			if n == 0 {
				return d.syntaxError("unexpected token in table key", i)
			}
			i += n
		}
		needSegment = false
	}
}

func (d *Decoder) scanQuoted(quote byte, off int) (int, error) {
	if off+2 < len(d.buf) && d.buf[off+1] == quote && d.buf[off+2] == quote {
		return 0, d.syntaxError("multiline quoted keys are not supported", off)
	}
	i := off + 1
	for i < len(d.buf) {
		var idx int
		if quote == '\'' {
			idx = scan.ScanLiteralString(d.buf[i:])
		} else {
			idx = scan.ScanBasicString(d.buf[i:])
		}
		if hasNewlineBefore(d.buf[i:], idx) {
			return 0, d.syntaxError("unterminated quoted key", off)
		}
		if idx >= len(d.buf[i:]) {
			return 0, d.syntaxError("unterminated quoted key", off)
		}
		if quote == '"' && d.buf[i+idx] == '\\' {
			if i+idx+1 >= len(d.buf) {
				return 0, d.syntaxError("unterminated quoted key", off)
			}
			i += idx + 2
			continue
		}
		if d.buf[i+idx] == quote {
			end := i + idx + 1
			if err := validateStringValue(d.buf[off:end]); err != nil {
				return 0, err
			}
			return end, nil
		}
	}
	return 0, d.syntaxError("unterminated quoted key", off)
}

//nolint:cyclop,funlen,gocognit // multiline/single-line string terminator scan; cohesive escape state machine.
func (d *Decoder) scanString(off int) (int, TokenKind, error) {
	rest := d.buf[off:]
	if hasBytePrefix(rest, '"', '"', '"') { //nolint:nestif // multiline basic-string terminator scan; cohesive.
		for end := off + 3; end < len(d.buf); {
			n := scan.ScanBasicString(d.buf[end:])
			end += n
			if end >= len(d.buf) {
				break
			}
			switch d.buf[end] {
			case '\\':
				if end+1 >= len(d.buf) {
					return 0, TokenKindInvalid, d.syntaxError("unterminated string escape", end)
				}
				end += 2
			case '"':
				run := countByteRun(d.buf[end:], '"')
				if run >= 3 {
					tokenEnd := end + min(run, 5)
					if err := validateStringValue(d.buf[off:tokenEnd]); err != nil {
						return 0, TokenKindInvalid, err
					}
					return tokenEnd, TokenKindValueString, nil
				}
				end += run
			default:
				end++
			}
		}
		return 0, TokenKindInvalid, d.syntaxError("unterminated multiline string", off)
	}
	if hasBytePrefix(rest, '\'', '\'', '\'') { //nolint:nestif // multiline literal-string terminator scan; cohesive.
		for end := off + 3; end < len(d.buf); {
			n := scan.ScanLiteralString(d.buf[end:])
			end += n
			if end >= len(d.buf) {
				break
			}
			run := countByteRun(d.buf[end:], '\'')
			if run >= 3 {
				tokenEnd := end + min(run, 5)
				if err := validateStringValue(d.buf[off:tokenEnd]); err != nil {
					return 0, TokenKindInvalid, err
				}
				return tokenEnd, TokenKindValueString, nil
			}
			end += run
		}
		return 0, TokenKindInvalid, d.syntaxError("unterminated multiline string", off)
	}
	quote := d.buf[off]
	if quote == '"' {
		// escapeFree tracks whether the body contains no backslash escape. The
		// ScanBasicStringStrict kernel only advances over bytes that are valid
		// unescaped basic-string content (it stops at control bytes, which the
		// default branch reports), so if the closing quote is reached without
		// ever taking the '\\' branch, the whole body is already proven valid
		// and the validateStringValue re-scan below is redundant. Escaped
		// strings still need validation to check escape-sequence correctness.
		escapeFree := true
		for i := off + 1; i < len(d.buf); {
			n := scan.ScanBasicStringStrict(d.buf[i:])
			i += n
			if i >= len(d.buf) {
				break
			}
			switch d.buf[i] {
			case '\n', '\r':
				return 0, TokenKindInvalid, d.syntaxError("unterminated string", off)
			case '\\':
				if i+1 >= len(d.buf) {
					return 0, TokenKindInvalid, d.syntaxError("unterminated string", off)
				}
				escapeFree = false
				i += 2
			case '"':
				end := i + 1
				if !escapeFree {
					if err := validateStringValue(d.buf[off:end]); err != nil {
						return 0, TokenKindInvalid, err
					}
				}
				return end, TokenKindValueString, nil
			default:
				return 0, TokenKindInvalid, stringControlError(d.buf[off+1:], i-off-1)
			}
		}
		return 0, TokenKindInvalid, d.syntaxError("unterminated string", off)
	}
	i := off + 1
	n := scan.ScanLiteralString(d.buf[i:])
	if hasNewlineBefore(d.buf[i:], n) {
		return 0, TokenKindInvalid, d.syntaxError("unterminated string", off)
	}
	i += n
	if i < len(d.buf) {
		end := i + 1
		if err := validateStringValue(d.buf[off:end]); err != nil {
			return 0, TokenKindInvalid, err
		}
		return end, TokenKindValueString, nil
	}
	return 0, TokenKindInvalid, d.syntaxError("unterminated string", off)
}

func countByteRun(raw []byte, b byte) int {
	n := 0
	for n < len(raw) && raw[n] == b {
		n++
	}
	return n
}

func hasBytePrefix(raw []byte, want ...byte) bool {
	if len(raw) < len(want) {
		return false
	}
	for i, b := range want {
		if raw[i] != b {
			return false
		}
	}
	return true
}

func hasNewlineBefore(raw []byte, end int) bool {
	if end > len(raw) {
		end = len(raw)
	}
	for _, ch := range raw[:end] {
		if ch == '\n' || ch == '\r' {
			return true
		}
	}
	return false
}

func (d *Decoder) enforceNestedDepth(off, depth int) {
	if d.limits.MaxNestedDepth > 0 && depth > d.limits.MaxNestedDepth {
		err := &LimitError{Limit: "MaxNestedDepth", Value: d.limits.MaxNestedDepth, Span: [2]int{off, off + 1}}
		d.setErr(err)
	}
}

func (d *Decoder) setErr(err error) {
	d.err = err
}

func (d *Decoder) syntaxError(msg string, off int) *SyntaxError {
	line, col := d.computeLineCol(off)
	err := &SyntaxError{Line: line, Col: col, Msg: msg, Span: [2]int{off, off + 1}}
	d.err = err
	return err
}

func (d *Decoder) computeLineCol(off int) (line, col int) {
	if d == nil {
		return 1, 1
	}
	if d.trackTokenPositions && off == d.off {
		return d.line, d.col
	}
	return lineColForOffset(d.buf, off)
}

func (d *Decoder) makeToken(kind TokenKind, raw []byte, offset int, scalar tokenScalar) rawToken {
	d.tokenScalar = scalar
	if d.trackTokenPositions {
		d.tokenLine, d.tokenCol = d.computeLineCol(offset)
	}
	return rawToken{Kind: kind, Bytes: raw, Offset: offset}
}

func lineColForOffset(data []byte, off int) (line, col int) {
	if off < 0 {
		off = 0
	}
	if off > len(data) {
		off = len(data)
	}
	prefix := data[:off]
	line = 1 + scan.CountLines(prefix)
	lineStart := 0
	if lastLF := bytes.LastIndexByte(prefix, '\n'); lastLF >= 0 {
		lineStart = lastLF + 1
	}
	col = 1
	for _, b := range prefix[lineStart:] {
		if b != '\r' {
			col++
		}
	}
	return line, col
}

func (d *Decoder) advanceOne() {
	d.advanceBytes(d.buf[d.off : d.off+1])
}

func (d *Decoder) advanceBytes(raw []byte) {
	if len(raw) == 0 {
		return
	}
	if d.trackTokenPositions {
		d.advancePosition(raw)
	}
	d.off += len(raw)
	d.atLineStart = false
	if d.off >= len(d.buf) {
		d.atLineStart = false
	}
}

func (d *Decoder) advancePosition(raw []byte) {
	if len(raw) == 1 {
		switch raw[0] {
		case '\n':
			d.line++
			d.col = 1
		case '\r':
		default:
			d.col++
		}
		return
	}
	lastLF := bytes.LastIndexByte(raw, '\n')
	if lastLF < 0 {
		d.col += displayColumnDelta(raw)
		return
	}
	d.line += scan.CountLines(raw)
	d.col = 1 + displayColumnDelta(raw[lastLF+1:])
}

func displayColumnDelta(raw []byte) int {
	if bytes.IndexByte(raw, '\r') < 0 {
		return len(raw)
	}
	n := 0
	for _, b := range raw {
		if b != '\r' {
			n++
		}
	}
	return n
}

func (d *Decoder) matchPrefix(prefix string) bool {
	if prefix == "" {
		return false
	}
	p := []byte(prefix)
	if d.off+len(p) > len(d.buf) {
		return false
	}
	for i := range p {
		if d.buf[d.off+i] != p[i] {
			return false
		}
	}
	return true
}

func (d *Decoder) skipBOM() {
	if len(d.buf)-d.off >= 3 && d.buf[d.off] == 0xEF && d.buf[d.off+1] == 0xBB && d.buf[d.off+2] == 0xBF {
		d.advanceBytes(d.buf[d.off : d.off+3])
	}
}

func looksLikeDatetime(raw []byte) bool {
	if !hasDateTimeShape(raw) {
		return false
	}
	_, _, err := parseDateTimeValue(raw)
	return err == nil
}

func hasDateTimeShape(raw []byte) bool {
	switch {
	case hasDateShape(raw):
		return true
	case hasTimeShape(raw):
		return true
	default:
		return false
	}
}

func hasDateShape(raw []byte) bool {
	if len(raw) < len("0000-00-00") {
		return false
	}
	return isDigit(raw[0]) &&
		isDigit(raw[1]) &&
		isDigit(raw[2]) &&
		isDigit(raw[3]) &&
		raw[4] == '-' &&
		isDigit(raw[5]) &&
		isDigit(raw[6]) &&
		raw[7] == '-' &&
		isDigit(raw[8]) &&
		isDigit(raw[9])
}

func hasTimeShape(raw []byte) bool {
	if len(raw) < len("00:00") {
		return false
	}
	return isDigit(raw[0]) &&
		isDigit(raw[1]) &&
		raw[2] == ':' &&
		isDigit(raw[3]) &&
		isDigit(raw[4])
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func scanBareValueEnd(raw []byte) int {
	end := scan.ScanBareValueEnd(raw)
	if end != len("0000-00-00") || end >= len(raw) || raw[end] != ' ' {
		return end
	}
	tailEnd := end + 1 + scan.ScanBareValueEnd(raw[end+1:])
	if _, _, err := parseDateTimeValue(raw[:tailEnd]); err == nil {
		return tailEnd
	}
	return end
}

func classifyBareValue(raw []byte) (TokenKind, tokenScalar, string) {
	switch {
	case looksLikeDatetime(raw):
		return TokenKindValueDatetime, tokenScalar{}, ""
	case bytes.ContainsAny(raw, "= "):
		return TokenKindInvalid, tokenScalar{}, "unexpected = in value"
	case bytes.Equal(raw, trueLiteral) || bytes.Equal(raw, falseLiteral):
		return TokenKindValueBool, tokenScalar{}, ""
	case isSpecialFloatBytes(raw):
		f, _ := parseSpecialFloatLiteral(raw)
		return TokenKindValueFloat, tokenScalar{bits: math.Float64bits(f), kind: tokenScalarFloat}, ""
	case isIntCandidateBytes(raw):
		if hasCapitalNumericPrefixBytes(raw) {
			return TokenKindInvalid, tokenScalar{}, "malformed value"
		}
		i, err := parseIntegerLiteral(raw)
		if err != nil {
			return TokenKindInvalid, tokenScalar{}, "malformed value"
		}
		return TokenKindValueInteger, tokenScalar{bits: uint64(i), kind: tokenScalarInteger}, "" //nolint:gosec // G115: i is an int64 stored as raw bits; round-tripped by scalarIntegerValue.
	case isFloatCandidateBytes(raw):
		f, err := parseFloatLiteral(raw)
		if err != nil {
			return TokenKindInvalid, tokenScalar{}, "malformed float"
		}
		return TokenKindValueFloat, tokenScalar{bits: math.Float64bits(f), kind: tokenScalarFloat}, ""
	case containsMalformedBareValueByte(raw):
		return TokenKindInvalid, tokenScalar{}, "malformed value"
	default:
		return TokenKindInvalid, tokenScalar{}, "malformed value"
	}
}

func bytesTrimRightSpaces(raw []byte) []byte {
	for len(raw) > 0 {
		switch raw[len(raw)-1] {
		case ' ', '\t':
			raw = raw[:len(raw)-1]
		default:
			return raw
		}
	}
	return raw
}

//nolint:cyclop // TOML integer-literal classification; cohesive sequence of guards.
func isIntCandidateBytes(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	n := trimSignBytes(raw)
	if len(n) == 0 {
		return false
	}
	if len(n) >= 2 && n[0] == '0' && n[1] == '_' {
		return false
	}
	if len(n) > 2 && n[0] == '0' {
		switch lowerASCII(n[1]) {
		case 'b', 'o', 'x':
			if raw[0] == '+' || raw[0] == '-' {
				return false
			}
			return prefixedDigitsValidBytes(n[2:], lowerASCII(n[1]))
		}
	}
	if len(n) > 1 && n[0] == '0' && n[1] >= '0' && n[1] <= '9' {
		return false
	}
	for _, b := range n {
		if (b >= '0' && b <= '9') || b == '_' {
			continue
		}
		return false
	}
	return hasValidNumberUnderscoresBytes(n)
}

func trimSignBytes(raw []byte) []byte {
	if len(raw) > 0 && (raw[0] == '+' || raw[0] == '-') {
		return raw[1:]
	}
	return raw
}

func prefixedDigitsValidBytes(raw []byte, prefix byte) bool {
	if len(raw) == 0 || raw[0] == '_' || raw[len(raw)-1] == '_' {
		return false
	}
	prevUnderscore := false
	for i, c := range raw {
		if c == '_' {
			if prevUnderscore || i == 0 || i+1 >= len(raw) || !validPrefixedDigit(raw[i-1], prefix) || !validPrefixedDigit(raw[i+1], prefix) {
				return false
			}
			prevUnderscore = true
			continue
		}
		prevUnderscore = false
		if !validPrefixedDigit(c, prefix) {
			return false
		}
	}
	return true
}

func validPrefixedDigit(c, prefix byte) bool {
	switch prefix {
	case 'b':
		return c == '0' || c == '1'
	case 'o':
		return c >= '0' && c <= '7'
	case 'x':
		return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
	default:
		return false
	}
}

func hasCapitalNumericPrefixBytes(raw []byte) bool {
	n := trimSignBytes(raw)
	return len(n) > 1 && n[0] == '0' && (n[1] == 'B' || n[1] == 'O' || n[1] == 'X')
}

func isSpecialFloatBytes(raw []byte) bool {
	switch {
	case bytes.Equal(raw, infLiteral), bytes.Equal(raw, posInfLiteral), bytes.Equal(raw, negInfLiteral):
		return true
	case bytes.Equal(raw, nanLiteral), bytes.Equal(raw, posNanLiteral), bytes.Equal(raw, negNanLiteral):
		return true
	default:
		return false
	}
}

//nolint:cyclop // TOML float-literal classification; cohesive scan over the byte class.
func isFloatCandidateBytes(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	n := trimSignBytes(raw)
	if len(n) == 0 {
		return false
	}
	if len(n) > 1 && n[0] == '0' {
		next := n[1]
		if (next >= '0' && next <= '9') || next == '_' {
			return false
		}
	}
	hasExp := false
	hasDecimalPoint := false
	for i := range n {
		switch n[i] {
		case '.':
			if i == 0 || hasDecimalPoint || i+1 >= len(n) || n[i+1] < '0' || n[i+1] > '9' {
				return false
			}
			hasDecimalPoint = true
		case 'e', 'E':
			if hasExp {
				return false
			}
			hasExp = true
		case '+', '-':
			if i == 0 || (n[i-1] != 'e' && n[i-1] != 'E') {
				return false
			}
		case '_':
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		default:
			return false
		}
	}
	if !hasDecimalPoint && !hasExp {
		return false
	}
	return hasValidNumberUnderscoresBytes(n)
}

func containsMalformedBareValueByte(raw []byte) bool {
	for _, b := range raw {
		if (b >= '0' && b <= '9') || b == '+' || b == '-' || b == '.' || b == '_' || b == ':' || b == 'e' || b == 'E' {
			continue
		}
		return true
	}
	return false
}

func hasValidNumberUnderscoresBytes(raw []byte) bool {
	if len(raw) == 0 || raw[0] == '_' || raw[len(raw)-1] == '_' {
		return false
	}
	prevUnderscore := false
	for i := range raw {
		if raw[i] != '_' {
			prevUnderscore = false
			continue
		}
		if prevUnderscore || i == 0 || i+1 >= len(raw) {
			return false
		}
		prev, next := raw[i-1], raw[i+1]
		if prev < '0' || prev > '9' {
			return false
		}
		if next < '0' || next > '9' {
			return false
		}
		prevUnderscore = true
	}
	return true
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
