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

//go:build !force_swar && arm64

package scan

import "unicode/utf8"

// scan_arm64.go contains the NEON variants of every scan kernel,
// implemented as hand-rolled Plan 9 assembly in scan_arm64.s. NEON
// (ASIMD) is ABI-guaranteed on every arm64 host, so the dispatch vars
// below bind to the NEON variants unconditionally — no runtime feature
// detect and no init() block is needed (T4 dispatch wiring is a no-op on
// this arch).
//
// # Lane reduction shape
//
// NEON variants use two related lane-reduction shapes. Legacy/small kernels
// may load 16 bytes and narrow one 0xFF/0x00 mask with VSHRN $4, while the
// hot paths use a 32-byte dual-vector loop ported from internal/memchr and
// bytealg practice: cheaply OR-reduce candidate masks on the no-hit path,
// then build the precise magic-constant syndrome only for the hit block.
//
// "Find first non-match" kernels build an invalid-byte mask before the
// same RBIT+CLZ locator step. ScanBareKey classifies bytes with low/high
// nibble VTBL bitset lookups; SkipWhitespace uses equality compares for
// membership and then inverts that mask with VNOT.
//
// # Tail handling
//
// Inputs not divisible by 16 bytes finish in a per-byte tail loop coded
// directly in assembly. No separate Tail entry points are exported.
//
// # ValidateUTF8
//
// Mirrors the amd64 ASCII-fast-path: validateUTF8NEONBulk (assembly)
// finds the first byte with the high bit set using a 32-byte dual-vector
// loop; validateUTF8NEON wraps it with a Go scalar continuation that calls
// unicode/utf8.DecodeRune for
// the multi-byte tail. A full SIMD UTF-8 state machine (Lemire/Keiser)
// would require a more elaborate PSHUFB-style table lookup that is not
// justified at this phase.

// Default unexported dispatch bindings. NEON is ABI-guaranteed on arm64
// so these are statically bound to the NEON variants; T4's dispatch
// wiring is a no-op on this arch (no runtime feature detect needed).
var (
	scanBareIdent         = scanBareKeyNEON
	scanBasicString       = scanBasicStringNEON
	scanBasicStringStrict = scanBasicStringStrictNEON
	scanCommentBody       = scanCommentBodyNEON
	scanBareValueEnd      = scanBareValueEndNEON
	countLines            = countLinesNEON
	scanLiteralString     = scanLiteralStringNEON
	skipWhitespace        = skipWhitespaceNEON
	locateNewline         = locateNewlineNEON
	validateUTF8          = validateUTF8NEON
)

// =====================================================================
// Assembly entry points (implemented in scan_arm64.s).
// =====================================================================

// scanBareKeyNEON is the NEON variant of ScanBareKey.
func scanBareKeyNEON(s []byte) int

// scanBasicStringNEON is the NEON variant of ScanBasicString.
func scanBasicStringNEON(s []byte) int

// scanLiteralStringNEON is the NEON variant of ScanLiteralString.
func scanLiteralStringNEON(s []byte) int

// scanBasicStringStrictNEON is the NEON variant of ScanBasicStringStrict.
func scanBasicStringStrictNEON(s []byte) int

// scanCommentBodyNEON is the NEON variant of ScanCommentBody.
func scanCommentBodyNEON(s []byte) int

// scanBareValueEndNEON is the NEON variant of ScanBareValueEnd.
func scanBareValueEndNEON(s []byte) int

// countLinesNEON is the NEON variant of CountLines.
func countLinesNEON(s []byte) int

// skipWhitespaceNEON is the NEON variant of SkipWhitespace.
func skipWhitespaceNEON(s []byte) int

// locateNewlineNEON is the NEON variant of LocateNewline. Returns -1
// (not len(s)) when no newline is present.
func locateNewlineNEON(s []byte) int

// validateUTF8NEONBulk returns the byte index of the first byte in s
// with the high bit set (>= 0x80), or len(s) if every byte in s is
// pure ASCII. Implemented in scan_arm64.s with a 32-byte NEON stride
// and a per-byte tail.
func validateUTF8NEONBulk(s []byte) int

// =====================================================================
// Go-side wrappers / scalar continuations.
// =====================================================================

// validateUTF8NEON wraps validateUTF8NEONBulk with a Go scalar
// continuation that uses unicode/utf8.DecodeRune to validate multi-byte
// sequences once the ASCII fast path encounters a high-bit byte. This
// mirrors the SSE2/AVX2 pattern from scan_amd64.go and sidesteps the
// complexity of a full SIMD UTF-8 state machine.
func validateUTF8NEON(s []byte) int {
	i := validateUTF8NEONBulk(s)
	if i == len(s) {
		return i
	}
	return i + validateUTF8NEONScalar(s[i:])
}

// validateUTF8NEONScalar is the non-SIMD continuation called from
// validateUTF8NEON once the ASCII fast path encounters a high-bit byte.
// It loops byte-by-byte, advancing by unicode/utf8.DecodeRune for
// multi-byte sequences and reporting the first byte of the first
// invalid sequence.
func validateUTF8NEONScalar(s []byte) int {
	i := 0
	for i < len(s) {
		b := s[i]
		if b < 0x80 {
			i++
			continue
		}
		r, size := utf8.DecodeRune(s[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return len(s)
}
