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

package scan

import "fmt"

// LimitError reports that a DoS-protection cap was exceeded by the parser
// layer that consumed one of the scan kernels.
//
// LimitError is constructed by the toml parser layer (decoder.go and
// value.go), not by the scan kernels themselves; the kernels in this
// package operate on caller-provided slices and never enforce DoS caps
// directly. The type
// lives here because the LimitError shape is shared by every consumer of
// the scan kernels and the cap enforcement is a property of the slice the
// scan kernel returns into (see AC-PAR-8).
type LimitError struct {
	// Limit names the configured cap that was violated. One of
	// "MaxNestedDepth", "MaxKeyLength", "MaxStringLength", or
	// "MaxDocumentSize".
	Limit string

	// Value is the configured numeric limit (the cap itself, not the
	// offending value).
	Value int

	// Span is the [start, end) byte offsets into the original source where
	// the violation was detected.
	Span [2]int
}

// Error reports the violated limit and its configured value.
func (e *LimitError) Error() string {
	return fmt.Sprintf("toml: %s cap exceeded (limit=%d, span=[%d,%d))",
		e.Limit, e.Value, e.Span[0], e.Span[1])
}

// ScanBareKey returns the count of leading bytes in s that belong to the
// TOML bare-key character class [A-Za-z0-9_-]. Returns 0 when s is empty
// or the first byte is not in the class; returns len(s) when every byte
// in s is in the class.
func ScanBareKey(s []byte) int { return scanBareIdent(s) } //nolint:revive // deliberate Scan* scanner-kernel family naming (see doc.go); internal pkg, renaming ripples across documented oracle/property tests.

// ScanBasicString returns the byte index of the first '"' or '\\' in s,
// or len(s) when neither byte appears. ScanBasicString is the
// basic-string inner-loop kernel: the decoder consumes the prefix
// returned by this call as a literal run, then dispatches on whichever of
// the two terminator bytes was found.
func ScanBasicString(s []byte) int { return scanBasicString(s) } //nolint:revive // deliberate Scan* scanner-kernel family naming (see doc.go); internal pkg, renaming ripples across documented oracle/property tests.

// ScanBasicStringStrict returns the byte index of the first byte that
// needs slow-path handling in a single-line TOML basic string: a double
// quote, a backslash, DEL (0x7f), or a C0 control byte below 0x20 other
// than tab. It returns len(s) when every byte is plain body text.
func ScanBasicStringStrict(s []byte) int { return scanBasicStringStrict(s) } //nolint:revive // deliberate Scan* scanner-kernel family naming (see doc.go); internal pkg, renaming ripples across documented oracle/property tests.

// ScanCommentBody returns the byte index of the first byte that needs
// parser-level handling in a TOML comment body: a line-feed, carriage
// return, DEL (0x7f), or a C0 control byte below 0x20 other than tab.
// It returns len(s) when every byte is valid comment body text.
func ScanCommentBody(s []byte) int { return scanCommentBody(s) } //nolint:revive // deliberate Scan* scanner-kernel family naming (see doc.go); internal pkg, renaming ripples across documented oracle/property tests.

// ScanBareValueEnd returns the byte index of the first TOML bare-value
// delimiter in s: space, tab, CR, LF, comma, right bracket, right brace,
// hash, or equals. It returns len(s) when no delimiter appears.
func ScanBareValueEnd(s []byte) int { return scanBareValueEnd(s) } //nolint:revive // deliberate Scan* scanner-kernel family naming (see doc.go); internal pkg, renaming ripples across documented oracle/property tests.

// CountLines returns the number of line-feed bytes ('\n') in s. It is
// intentionally LF-only: callers that care about CRLF or lone CR retain
// those grammar decisions outside this byte-count kernel.
func CountLines(s []byte) int { return countLines(s) }

// ScanLiteralString returns the byte index of the first single-quote
// byte (0x27) in s, or len(s) when the byte is absent. Literal strings
// perform no escape processing, so a single byte class suffices.
func ScanLiteralString(s []byte) int { return scanLiteralString(s) } //nolint:revive // deliberate Scan* scanner-kernel family naming (see doc.go); internal pkg, renaming ripples across documented oracle/property tests.

// SkipWhitespace returns the count of leading ' ' (U+0020) or '\t'
// (U+0009) bytes in s. Newline ('\n', U+000A) is intentionally NOT
// whitespace for this scanner — newlines are statement separators in
// TOML and are reported by LocateNewline.
func SkipWhitespace(s []byte) int { return skipWhitespace(s) }

// LocateNewline returns the byte index of the first '\n' (U+000A) in s,
// or -1 when the byte is absent. The -1 sentinel intentionally differs
// from the rest of the family (len(s)) — newlines are statement
// boundaries, so callers commonly need the "no boundary in this slice"
// branch to be distinct from the "boundary at end" branch.
func LocateNewline(s []byte) int { return locateNewline(s) }

// ValidateUTF8 returns the byte index of the first invalid UTF-8
// sequence start in s, or len(s) when every byte sequence in s is valid
// UTF-8. The returned index always points to the first byte of the
// offending sequence (not a continuation byte mid-sequence), which is
// the offset the parser must reset its position to in order to report a
// clean line/column.
func ValidateUTF8(s []byte) int { return validateUTF8(s) }
