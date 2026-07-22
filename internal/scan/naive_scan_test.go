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

import (
	"testing"
	"unicode/utf8"
)

// naive_scan_test.go is the load-bearing correctness oracle for every
// dispatched implementation in this package. The naiveX functions
// below are intentionally the most-obvious, byte-by-byte reference
// implementations of the scan kernels — they are easy to inspect, and
// they double as the AC-SIMD-5 baseline for the class-scan kernels
// (ScanBareKey, ScanBasicString, SkipWhitespace).
//
// THIS FILE INTENTIONALLY HAS NO BUILD TAG. It compiles for every
// (GOARCH, GOOS, goexperiment.simd) combination, so every backend test
// can call into it.

// naiveScanBareKey is the byte-by-byte oracle for ScanBareKey. It counts
// the leading bytes of s that match [A-Za-z0-9_-].
func naiveScanBareKey(s []byte) int {
	for i, b := range s {
		switch {
		case b >= 'A' && b <= 'Z':
		case b >= 'a' && b <= 'z':
		case b >= '0' && b <= '9':
		case b == '_' || b == '-':
		default:
			return i
		}
	}
	return len(s)
}

// naiveScanBasicString is the byte-by-byte oracle for ScanBasicString.
// It returns the index of the first '"' or '\\' byte in s, or len(s) if
// neither byte is present.
func naiveScanBasicString(s []byte) int {
	for i, b := range s {
		if b == '"' || b == '\\' {
			return i
		}
	}
	return len(s)
}

func naiveScanBasicStringEscape(s []byte) int {
	for i, b := range s {
		if basicStringEscapeStop(b) {
			return i
		}
	}
	return len(s)
}

// naiveScanBasicStringStrict is the byte-by-byte oracle for
// ScanBasicStringStrict. It returns the first byte that needs slow-path
// handling in a single-line TOML basic string: a double quote,
// backslash, DEL, or a C0 control byte below 0x20 other than tab.
func naiveScanBasicStringStrict(s []byte) int {
	for i, b := range s {
		if basicStringStrictStop(b) {
			return i
		}
	}
	return len(s)
}

// naiveScanCommentBody is the byte-by-byte oracle for ScanCommentBody.
// It returns the first line terminator or prohibited comment control byte.
func naiveScanCommentBody(s []byte) int {
	for i, b := range s {
		if commentBodyStop(b) {
			return i
		}
	}
	return len(s)
}

// naiveScanBareValueEnd is the byte-by-byte oracle for ScanBareValueEnd.
// It returns the first bare-value delimiter byte or len(s) if absent.
func naiveScanBareValueEnd(s []byte) int {
	for i, b := range s {
		if bareValueDelimiter(b) {
			return i
		}
	}
	return len(s)
}

// naiveCountLines is the byte-by-byte oracle for CountLines. It counts
// line-feed bytes only and intentionally ignores carriage returns.
func naiveCountLines(s []byte) int {
	n := 0
	for _, b := range s {
		if b == '\n' {
			n++
		}
	}
	return n
}

// naiveScanLiteralString is the byte-by-byte oracle for
// ScanLiteralString. It returns the index of the first single-quote
// byte (0x27) in s, or len(s) if absent.
func naiveScanLiteralString(s []byte) int {
	for i, b := range s {
		if b == '\'' {
			return i
		}
	}
	return len(s)
}

// naiveSkipWhitespace is the byte-by-byte oracle for SkipWhitespace. It
// counts leading ' ' or '\t' bytes; newline ('\n') is intentionally NOT
// whitespace.
func naiveSkipWhitespace(s []byte) int {
	for i, b := range s {
		if b != ' ' && b != '\t' {
			return i
		}
	}
	return len(s)
}

// naiveLocateNewline is the byte-by-byte oracle for LocateNewline. It
// returns the index of the first '\n' byte in s, or -1 if absent.
func naiveLocateNewline(s []byte) int {
	for i, b := range s {
		if b == '\n' {
			return i
		}
	}
	return -1
}

// naiveValidateUTF8 is the byte-by-byte oracle for ValidateUTF8. It
// returns the index of the first invalid UTF-8 sequence start in s, or
// len(s) if every byte sequence is valid UTF-8.
func naiveValidateUTF8(s []byte) int {
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRune(s[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return len(s)
}

func TestValidateUTF8_reportsInvalidSequenceStart_forEveryInvalidClass(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{name: "overlong 2-byte NUL", data: []byte{0xC0, 0x80}, want: 0},
		{name: "overlong 3-byte NUL", data: []byte{0xE0, 0x80, 0x80}, want: 0},
		{name: "overlong 4-byte NUL", data: []byte{0xF0, 0x80, 0x80, 0x80}, want: 0},
		{name: "surrogate high half", data: []byte{0xED, 0xA0, 0x80}, want: 0},
		{name: "surrogate low half", data: []byte{0xED, 0xBF, 0xBF}, want: 0},
		{name: "above U+10FFFF", data: []byte{0xF4, 0x90, 0x80, 0x80}, want: 0},
		{name: "truncated 2-byte", data: []byte{'a', 0xC2}, want: 1},
		{name: "truncated 3-byte", data: []byte{'a', 0xE2, 0x82}, want: 1},
		{name: "truncated 4-byte", data: []byte{'a', 0xF0, 0x9D, 0x84}, want: 1},
		{name: "lone continuation", data: []byte{'a', 0x80}, want: 1},
		{name: "2-byte lead without continuation", data: []byte{'a', 0xC2, 'b'}, want: 1},
		{name: "3-byte lead without continuation", data: []byte{'a', 0xE2, 'b', 0x80}, want: 1},
		{name: "4-byte lead without continuation", data: []byte{'a', 0xF0, 'b', 0x80, 0x80}, want: 1},
		{name: "invalid after valid multibyte", data: []byte("ok 世界\x80"), want: len([]byte("ok 世界"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateUTF8(tt.data)
			if got != tt.want {
				t.Fatalf("ValidateUTF8(%x) = %d, want %d", tt.data, got, tt.want)
			}
			if oracle := naiveValidateUTF8(tt.data); oracle != tt.want {
				t.Fatalf("naiveValidateUTF8(%x) = %d, want %d", tt.data, oracle, tt.want)
			}
		})
	}
}
