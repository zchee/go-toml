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
	"fmt"
)

// SyntaxError reports parse errors with line/column and byte span context.
type SyntaxError struct {
	// Line is 1-based.
	Line int
	// Col is 1-based.
	Col int

	// Msg is a parser-specific message.
	Msg string

	// Span is the offending [start, end) byte range in source.
	Span [2]int
}

// Error implements the error interface.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("toml: %s at line %d col %d", e.Msg, e.Line, e.Col)
}

func syntaxErrorAtOffset(data []byte, off int, msg string, span [2]int) *SyntaxError {
	line, col := lineColForOffset(data, off)
	if len(data) == 0 && off > 0 {
		line, col = 1, off+1
	}
	return &SyntaxError{Line: line, Col: col, Msg: msg, Span: span}
}

func syntaxErrorForRawToken(data []byte, tok rawToken, msg string) *SyntaxError {
	return syntaxErrorAtOffset(data, tok.Offset, msg, rawTokenSpan(tok))
}

func decoderSyntaxErrorForRawToken(dec *Decoder, tok rawToken, msg string) *SyntaxError {
	if dec == nil {
		return syntaxErrorForRawToken(nil, tok, msg)
	}
	return syntaxErrorForRawToken(dec.buf, tok, msg)
}

func decoderSource(dec *Decoder) []byte {
	if dec == nil {
		return nil
	}
	return dec.buf
}

func tokenSpan(tok Token) [2]int {
	return spanForToken(tok.Offset, len(tok.Bytes))
}

func rawTokenSpan(tok rawToken) [2]int {
	return spanForToken(tok.Offset, len(tok.Bytes))
}

func spanForToken(offset, length int) [2]int {
	end := offset + length
	if end <= offset {
		end = offset + 1
	}
	return [2]int{offset, end}
}

// LimitError reports DoS-defense cap violations.
type LimitError struct {
	// Limit is one of MaxNestedDepth, MaxKeyLength, MaxStringLength,
	// or MaxDocumentSize.
	Limit string

	// Value is the configured cap value.
	Value int

	// Span is the offending [start, end) byte range in source.
	Span [2]int
}

// Error implements the error interface.
func (e *LimitError) Error() string {
	return fmt.Sprintf("toml: %s cap exceeded (limit=%d, span=[%d,%d))", e.Limit, e.Value, e.Span[0], e.Span[1])
}

// DecoderStateError reports a high-level decode operation attempted after the
// decoder token stream had already been consumed.
type DecoderStateError struct {
	// Offset is the current byte offset of the decoder.
	Offset int
}

// Error implements the error interface.
func (e *DecoderStateError) Error() string {
	return fmt.Sprintf("toml: decoder already consumed input at byte offset %d", e.Offset)
}

// LocalTimeIntoTimeError reports an unsafe conversion from a TOML local datetime
// form into time.Time without an explicit local-as-UTC option.
type LocalTimeIntoTimeError struct {
	// Kind is the token kind that carried the local datetime source.
	Kind TokenKind

	// Source is the raw TOML datetime source.
	Source []byte

	// Span is the offending [start, end) byte range in source.
	Span [2]int
}

// Error implements the error interface.
func (e *LocalTimeIntoTimeError) Error() string {
	return "toml: local datetime cannot decode into time.Time without WithLocalAsUTC"
}

// TagOptionError reports unsupported toml struct-tag options.
type TagOptionError struct {
	Struct string
	Field  string
	Option string
}

// Error implements the error interface.
func (e *TagOptionError) Error() string {
	return fmt.Sprintf("toml: unsupported struct tag option %q on %s.%s", e.Option, e.Struct, e.Field)
}

// UnsupportedTypeError reports values that cannot be represented as TOML.
type UnsupportedTypeError struct {
	Type string
}

// Error implements the error interface.
func (e *UnsupportedTypeError) Error() string {
	return "toml: unsupported type " + e.Type
}

// TypeMismatchError reports a TOML value that cannot bind to the target type.
type TypeMismatchError struct {
	Path string
	Want string
	Got  string
}

// Error implements the error interface.
func (e *TypeMismatchError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("toml: cannot decode %s into %s", e.Got, e.Want)
	}
	return fmt.Sprintf("toml: cannot decode %s into %s at %s", e.Got, e.Want, e.Path)
}
