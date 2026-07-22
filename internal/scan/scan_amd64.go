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

//go:build !force_swar && goexperiment.simd && amd64

package scan

import (
	"math/bits"
	"simd/archsimd"
)

// scan_amd64.go contains the SSE2 (16-byte) and AVX2 (32-byte) variants
// of every scan kernel, implemented via simd/archsimd intrinsics — never
// hand-rolled Plan 9 assembly. This matches the internal/memchr precedent
// which proved that compiler-lowered intrinsics beat hand-rolled .s on
// amd64 across every metric.
//
// # Dispatch
//
// The package-level vars scanBareIdent, scanBasicString, scanLiteralString,
// skipWhitespace, locateNewline, validateUTF8 are statically bound here
// to the SSE2 variants. The init() block below (T4 dispatch wiring)
// rebinds them to the AVX2 variants when archsimd.X86.AVX2() returns
// true; otherwise they keep the SSE2 bindings, which the amd64 Go ABI
// guarantees are usable on every amd64 host (Go 1.18+ baseline).
//
// SSE2 is chosen as the static default precisely because of that ABI
// guarantee — it keeps the package correct on every amd64 host before
// the init() block runs and on hosts that lack AVX2 entirely.
//
// The `!force_swar` clause in the build tag makes this file vanish under
// `go {build,test} -tags=force_swar` so the SWAR backend declared in
// scan_swar.go (whose own tag expands to include force_swar) provides
// the dispatch vars instead. That AC-SIMD-7 verification path lets a
// host with native AVX2 still exercise the SWAR kernels for cross-
// backend equivalence testing.
//
// # Lane reduction shape
//
// Most kernels here are written in archsimd-intrinsics Go: each "find
// first match" kernel (ScanBasicString) computes a mask via vector
// Equal/Or, extracts a per-lane bitmask via Mask.ToBits, and finds the
// first match with math/bits.TrailingZeros. Each "find first non-match"
// kernel (ScanBareKey, SkipWhitespace) inverts the bitmask before the
// TrailingZeros call (first 0 in mask == first 1 in ~mask).
//
// LocateNewline and ScanLiteralString are the exception: their SSE2 and
// AVX2 variants are hand-rolled Plan 9 assembly in
// scan_amd64_single_byte.s. T5's AC-SIMD-5 perf gate (Task #7 / T2.1
// re-open) found the archsimd-intrinsics path losing to stdlib
// `bytes.IndexByte`, which uses fused single-TEXT
// PCMPEQB+PMOVMSKB+BSFL (SSE2) and VPCMPEQB+VPTEST+VPMOVMSKB (AVX2)
// inner loops. The .s variants follow stdlib indexbyte_amd64.s's
// structure verbatim so the comparison is like-for-like.
//
// ValidateUTF8 uses an ASCII-fast-path: any 32-byte (AVX2) or 16-byte
// (SSE2) chunk whose every byte has the high bit clear is trivially
// valid UTF-8, advance and continue; the first chunk with a high-bit
// byte falls through to a scalar utf8.DecodeRune loop. A full SIMD
// state machine (Lemire/Keiser) would be possible but requires PSHUFB
// and saturated arithmetic patterns that the perf gate in T5 will
// decide whether to invest in.
//
// # Tail handling
//
// AVX2 (32-byte stride) finishes its <32-byte tail by calling the SSE2
// variant on the remainder. SSE2 (16-byte stride) finishes its
// <16-byte tail with an inline scalar loop. No separate Tail helpers
// are exposed — keeping the per-scan call shape narrow.

// Default unexported dispatch bindings. The init() below rebinds these
// to the AVX2 variants when archsimd.X86.AVX2() returns true; otherwise
// they keep the SSE2 bindings declared at package init, which the amd64
// Go ABI guarantees are usable on every amd64 host.
var (
	scanBareIdent         = scanBareKeySSE2
	scanBasicString       = scanBasicStringSSE2
	scanBasicStringStrict = scanBasicStringStrictSSE2
	scanCommentBody       = scanCommentBodySSE2
	scanBareValueEnd      = scanBareValueEndSSE2
	countLines            = countLinesSSE2
	scanLiteralString     = scanLiteralStringSSE2
	skipWhitespace        = skipWhitespaceSSE2
	locateNewline         = locateNewlineSSE2
	validateUTF8          = validateUTF8SSE2
)

// init performs the amd64 runtime dispatch step owned by T4. It uses the
// archsimd.X86 capability struct (the same surface internal stdlib code
// uses to detect AVX2 at runtime) to decide whether the AVX2 32-byte
// variants are safe to bind. When archsimd.X86.AVX2() is false the
// package keeps the SSE2 bindings declared above — those are valid on
// every amd64 host the Go ABI supports.
//
// No abstraction or wrapper is interposed: each rebind is a direct
// assignment, mirroring the internal/memchr dispatch precedent. This
// keeps the calling convention identical between dispatch and the
// kernels and lets the compiler inline the funcptr indirection through
// devirtualization when the call site is hot.
func init() {
	if archsimd.X86.AVX2() {
		scanBareIdent = scanBareKeyAVX2
		scanBasicString = scanBasicStringAVX2
		scanBasicStringStrict = scanBasicStringStrictAVX2
		scanCommentBody = scanCommentBodyAVX2
		scanBareValueEnd = scanBareValueEndAVX2
		countLines = countLinesAVX2
		scanLiteralString = scanLiteralStringAVX2
		skipWhitespace = skipWhitespaceAVX2
		locateNewline = locateNewlineAVX2
		validateUTF8 = validateUTF8AVX2
	}
}

// =====================================================================
// AVX2 variants (32-byte stride)
// =====================================================================

func scanBareKeyAVX2(s []byte) int {
	i := 0
	upA := archsimd.BroadcastUint8x32('A')
	upZ := archsimd.BroadcastUint8x32('Z')
	loA := archsimd.BroadcastUint8x32('a')
	loZ := archsimd.BroadcastUint8x32('z')
	d0 := archsimd.BroadcastUint8x32('0')
	d9 := archsimd.BroadcastUint8x32('9')
	un := archsimd.BroadcastUint8x32('_')
	hy := archsimd.BroadcastUint8x32('-')
	for i+32 <= len(s) {
		v := archsimd.LoadUint8x32Slice(s[i:])
		isU := v.GreaterEqual(upA).And(v.LessEqual(upZ))
		isL := v.GreaterEqual(loA).And(v.LessEqual(loZ))
		isD := v.GreaterEqual(d0).And(v.LessEqual(d9))
		m := isU.Or(isL).Or(isD).Or(v.Equal(un)).Or(v.Equal(hy))
		b := m.ToBits()
		if b != 0xFFFFFFFF {
			return i + bits.TrailingZeros32(^b)
		}
		i += 32
	}
	return i + scanBareKeySSE2(s[i:])
}

func scanBasicStringAVX2(s []byte) int {
	i := 0
	quote := archsimd.BroadcastUint8x32('"')
	bksl := archsimd.BroadcastUint8x32('\\')
	for i+32 <= len(s) {
		v := archsimd.LoadUint8x32Slice(s[i:])
		m := v.Equal(quote).Or(v.Equal(bksl))
		if b := m.ToBits(); b != 0 {
			return i + bits.TrailingZeros32(b)
		}
		i += 32
	}
	return i + scanBasicStringSSE2(s[i:])
}

func scanBasicStringStrictAVX2(s []byte) int {
	i := 0
	quote := archsimd.BroadcastUint8x32('"')
	bksl := archsimd.BroadcastUint8x32('\\')
	del := archsimd.BroadcastUint8x32(0x7f)
	control := archsimd.BroadcastUint8x32(0x1f)
	for i+32 <= len(s) {
		v := archsimd.LoadUint8x32Slice(s[i:])
		m := v.Equal(quote).Or(v.Equal(bksl)).Or(v.Equal(del)).Or(v.LessEqual(control))
		if m.ToBits() != 0 {
			if n := scanBasicStringStrictScalar(s[i : i+32]); n < 32 {
				return i + n
			}
		}
		i += 32
	}
	return i + scanBasicStringStrictSSE2(s[i:])
}

func scanCommentBodyAVX2(s []byte) int {
	i := 0
	del := archsimd.BroadcastUint8x32(0x7f)
	control := archsimd.BroadcastUint8x32(0x1f)
	for i+32 <= len(s) {
		v := archsimd.LoadUint8x32Slice(s[i:])
		m := v.Equal(del).Or(v.LessEqual(control))
		if m.ToBits() != 0 {
			if n := scanCommentBodyScalar(s[i : i+32]); n < 32 {
				return i + n
			}
		}
		i += 32
	}
	return i + scanCommentBodySSE2(s[i:])
}

func scanBareValueEndAVX2(s []byte) int {
	i := 0
	sp := archsimd.BroadcastUint8x32(' ')
	tab := archsimd.BroadcastUint8x32('\t')
	cr := archsimd.BroadcastUint8x32('\r')
	lf := archsimd.BroadcastUint8x32('\n')
	comma := archsimd.BroadcastUint8x32(',')
	rbracket := archsimd.BroadcastUint8x32(']')
	rbrace := archsimd.BroadcastUint8x32('}')
	hash := archsimd.BroadcastUint8x32('#')
	eq := archsimd.BroadcastUint8x32('=')
	for i+32 <= len(s) {
		v := archsimd.LoadUint8x32Slice(s[i:])
		m := v.Equal(sp).Or(v.Equal(tab)).Or(v.Equal(cr)).Or(v.Equal(lf)).
			Or(v.Equal(comma)).Or(v.Equal(rbracket)).Or(v.Equal(rbrace)).Or(v.Equal(hash)).Or(v.Equal(eq))
		if b := m.ToBits(); b != 0 {
			return i + bits.TrailingZeros32(b)
		}
		i += 32
	}
	return i + scanBareValueEndSSE2(s[i:])
}

func countLinesAVX2(s []byte) int {
	i := 0
	n := 0
	lf := archsimd.BroadcastUint8x32('\n')
	for i+32 <= len(s) {
		v := archsimd.LoadUint8x32Slice(s[i:])
		n += bits.OnesCount32(v.Equal(lf).ToBits())
		i += 32
	}
	return n + countLinesSSE2(s[i:])
}

// scanLiteralStringAVX2 is implemented in scan_amd64_single_byte.s.
//
//go:noescape
func scanLiteralStringAVX2(s []byte) int

func skipWhitespaceAVX2(s []byte) int {
	i := 0
	sp := archsimd.BroadcastUint8x32(' ')
	tab := archsimd.BroadcastUint8x32('\t')
	for i+32 <= len(s) {
		v := archsimd.LoadUint8x32Slice(s[i:])
		m := v.Equal(sp).Or(v.Equal(tab))
		b := m.ToBits()
		if b != 0xFFFFFFFF {
			return i + bits.TrailingZeros32(^b)
		}
		i += 32
	}
	return i + skipWhitespaceSSE2(s[i:])
}

// locateNewlineAVX2 is implemented in scan_amd64_single_byte.s.
//
//go:noescape
func locateNewlineAVX2(s []byte) int

func validateUTF8AVX2(s []byte) int {
	i := validateUTF8ASCIIAVX2(s)
	if i == len(s) {
		return len(s)
	}
	return i + validateUTF8Scalar(s[i:])
}

func validateUTF8ASCIIAVX2(s []byte) int {
	i := 0
	hi := archsimd.BroadcastUint8x32(0x80)
	for i+32 <= len(s) {
		v := archsimd.LoadUint8x32Slice(s[i:])
		m := v.GreaterEqual(hi)
		if m.ToBits() == 0 {
			i += 32
			continue
		}
		return i + bits.TrailingZeros32(m.ToBits())
	}
	return i + validateUTF8ASCIISSE2(s[i:])
}

// =====================================================================
// SSE2 variants (16-byte stride). Each finishes its tail with an inline
// scalar loop; the AVX2 variants call these for their <32-byte tail.
// =====================================================================

func scanBareKeySSE2(s []byte) int {
	i := 0
	upA := archsimd.BroadcastUint8x16('A')
	upZ := archsimd.BroadcastUint8x16('Z')
	loA := archsimd.BroadcastUint8x16('a')
	loZ := archsimd.BroadcastUint8x16('z')
	d0 := archsimd.BroadcastUint8x16('0')
	d9 := archsimd.BroadcastUint8x16('9')
	un := archsimd.BroadcastUint8x16('_')
	hy := archsimd.BroadcastUint8x16('-')
	for i+16 <= len(s) {
		v := archsimd.LoadUint8x16Slice(s[i:])
		isU := v.GreaterEqual(upA).And(v.LessEqual(upZ))
		isL := v.GreaterEqual(loA).And(v.LessEqual(loZ))
		isD := v.GreaterEqual(d0).And(v.LessEqual(d9))
		m := isU.Or(isL).Or(isD).Or(v.Equal(un)).Or(v.Equal(hy))
		b := m.ToBits()
		if b != 0xFFFF {
			return i + bits.TrailingZeros16(^b)
		}
		i += 16
	}
	for ; i < len(s); i++ {
		b := s[i]
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

func scanBasicStringSSE2(s []byte) int {
	i := 0
	quote := archsimd.BroadcastUint8x16('"')
	bksl := archsimd.BroadcastUint8x16('\\')
	for i+16 <= len(s) {
		v := archsimd.LoadUint8x16Slice(s[i:])
		m := v.Equal(quote).Or(v.Equal(bksl))
		if b := m.ToBits(); b != 0 {
			return i + bits.TrailingZeros16(b)
		}
		i += 16
	}
	for ; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			return i
		}
	}
	return len(s)
}

func scanBasicStringStrictSSE2(s []byte) int {
	i := 0
	quote := archsimd.BroadcastUint8x16('"')
	bksl := archsimd.BroadcastUint8x16('\\')
	del := archsimd.BroadcastUint8x16(0x7f)
	control := archsimd.BroadcastUint8x16(0x1f)
	for i+16 <= len(s) {
		v := archsimd.LoadUint8x16Slice(s[i:])
		m := v.Equal(quote).Or(v.Equal(bksl)).Or(v.Equal(del)).Or(v.LessEqual(control))
		if m.ToBits() != 0 {
			if n := scanBasicStringStrictScalar(s[i : i+16]); n < 16 {
				return i + n
			}
		}
		i += 16
	}
	return i + scanBasicStringStrictScalar(s[i:])
}

func scanCommentBodySSE2(s []byte) int {
	i := 0
	del := archsimd.BroadcastUint8x16(0x7f)
	control := archsimd.BroadcastUint8x16(0x1f)
	for i+16 <= len(s) {
		v := archsimd.LoadUint8x16Slice(s[i:])
		m := v.Equal(del).Or(v.LessEqual(control))
		if m.ToBits() != 0 {
			if n := scanCommentBodyScalar(s[i : i+16]); n < 16 {
				return i + n
			}
		}
		i += 16
	}
	return i + scanCommentBodyScalar(s[i:])
}

func scanBareValueEndSSE2(s []byte) int {
	i := 0
	sp := archsimd.BroadcastUint8x16(' ')
	tab := archsimd.BroadcastUint8x16('\t')
	cr := archsimd.BroadcastUint8x16('\r')
	lf := archsimd.BroadcastUint8x16('\n')
	comma := archsimd.BroadcastUint8x16(',')
	rbracket := archsimd.BroadcastUint8x16(']')
	rbrace := archsimd.BroadcastUint8x16('}')
	hash := archsimd.BroadcastUint8x16('#')
	eq := archsimd.BroadcastUint8x16('=')
	for i+16 <= len(s) {
		v := archsimd.LoadUint8x16Slice(s[i:])
		m := v.Equal(sp).Or(v.Equal(tab)).Or(v.Equal(cr)).Or(v.Equal(lf)).
			Or(v.Equal(comma)).Or(v.Equal(rbracket)).Or(v.Equal(rbrace)).Or(v.Equal(hash)).Or(v.Equal(eq))
		if b := m.ToBits(); b != 0 {
			return i + bits.TrailingZeros16(b)
		}
		i += 16
	}
	return i + scanBareValueEndScalar(s[i:])
}

func countLinesSSE2(s []byte) int {
	i := 0
	n := 0
	lf := archsimd.BroadcastUint8x16('\n')
	for i+16 <= len(s) {
		v := archsimd.LoadUint8x16Slice(s[i:])
		n += bits.OnesCount16(v.Equal(lf).ToBits())
		i += 16
	}
	return n + countLinesScalar(s[i:])
}

// scanLiteralStringSSE2 is implemented in scan_amd64_single_byte.s.
//
//go:noescape
func scanLiteralStringSSE2(s []byte) int

func skipWhitespaceSSE2(s []byte) int {
	i := 0
	sp := archsimd.BroadcastUint8x16(' ')
	tab := archsimd.BroadcastUint8x16('\t')
	for i+16 <= len(s) {
		v := archsimd.LoadUint8x16Slice(s[i:])
		m := v.Equal(sp).Or(v.Equal(tab))
		b := m.ToBits()
		if b != 0xFFFF {
			return i + bits.TrailingZeros16(^b)
		}
		i += 16
	}
	for ; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return i
		}
	}
	return len(s)
}

// locateNewlineSSE2 is implemented in scan_amd64_single_byte.s.
//
//go:noescape
func locateNewlineSSE2(s []byte) int

func validateUTF8SSE2(s []byte) int {
	i := validateUTF8ASCIISSE2(s)
	if i == len(s) {
		return len(s)
	}
	return i + validateUTF8Scalar(s[i:])
}

func validateUTF8ASCIISSE2(s []byte) int {
	i := 0
	hi := archsimd.BroadcastUint8x16(0x80)
	for i+16 <= len(s) {
		v := archsimd.LoadUint8x16Slice(s[i:])
		m := v.GreaterEqual(hi)
		if m.ToBits() == 0 {
			i += 16
			continue
		}
		return i + bits.TrailingZeros16(m.ToBits())
	}
	for ; i < len(s); i++ {
		if s[i] >= 0x80 {
			return i
		}
	}
	return len(s)
}

func validateUTF8Scalar(s []byte) int {
	i := 0
	for i < len(s) {
		size := validateUTF8SequenceSize(s, i)
		if size == 0 {
			return i
		}
		i += size
	}
	return len(s)
}

func validateUTF8SequenceSize(s []byte, i int) int {
	b0 := s[i]
	switch {
	case b0 < 0x80:
		return 1
	case b0 < 0xC2:
		return 0
	case b0 < 0xE0:
		if i+1 < len(s) && utf8Continuation(s[i+1]) {
			return 2
		}
		return 0
	case b0 < 0xF0:
		if i+2 >= len(s) {
			return 0
		}
		b1 := s[i+1]
		if !utf8Continuation(b1) || !utf8Continuation(s[i+2]) {
			return 0
		}
		if b0 == 0xE0 && b1 < 0xA0 {
			return 0
		}
		if b0 == 0xED && b1 >= 0xA0 {
			return 0
		}
		return 3
	case b0 < 0xF5:
		if i+3 >= len(s) {
			return 0
		}
		b1 := s[i+1]
		if !utf8Continuation(b1) || !utf8Continuation(s[i+2]) || !utf8Continuation(s[i+3]) {
			return 0
		}
		if b0 == 0xF0 && b1 < 0x90 {
			return 0
		}
		if b0 == 0xF4 && b1 >= 0x90 {
			return 0
		}
		return 4
	default:
		return 0
	}
}

func utf8Continuation(b byte) bool { return b >= 0x80 && b <= 0xBF }
