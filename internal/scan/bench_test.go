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
	"bytes"
	rand "math/rand/v2"
	"strconv"
	"testing"
	"unicode/utf8"
)

// bench_test.go contains the AC-SIMD-5 per-scan benchmark pairs (one
// SIMD-through-dispatch and one declared-baseline benchmark per kernel,
// 20 benchmarks total). Each pair runs on the same 64 KB deterministic
// buffer so the SIMD/baseline ratio reported by benchstat is the
// numerical gate that the perf-gate invoker consumes.
//
// # AC-SIMD-5 baseline table
//
//   Scan                | Baseline                              | Gate ratio
//   --------------------|---------------------------------------|-----------
//   LocateNewline       | bytes.IndexByte(s, '\n')      (stdlib) | 0.98
//   ScanLiteralString   | bytes.IndexByte(s, '\'')      (stdlib) | 0.98
//   ScanBareKey         | naiveScanBareKey              (oracle) | 1.00
//   ScanBasicString     | naiveScanBasicString          (oracle) | 1.00
//   ScanBasicStringStrict| naiveScanBasicStringStrict   (oracle) | 1.00
//   ScanCommentBody     | naiveScanCommentBody          (oracle) | 1.00
//   ScanBareValueEnd    | naiveScanBareValueEnd         (oracle) | 1.00
//   CountLines          | naiveCountLines               (oracle) | 1.00
//   SkipWhitespace      | naiveSkipWhitespace           (oracle) | 1.00
//   ValidateUTF8        | utf8.Valid(s) wrapped to int  (stdlib) | 1.00
//
// The naive-loop baselines are the EXACT same code as the
// correctness oracles in naive_scan_test.go — one source of truth for
// both perf comparator and correctness verifier. The utf8.Valid
// baseline is wrapped via validateUTF8StdlibBaseline below to return
// an int (the contract the bench compares against), since utf8.Valid
// returns bool; the wrapper does NO additional scan work on the valid
// path (it just returns len(s)), so the wall-clock cost is dominated
// by utf8.Valid's own scan.
//
// # Bench protocol (plan §Cross-cutting > Bench protocol)
//
// The runtime knobs benchstat needs (GOMAXPROCS=1, empty GODEBUG,
// -count=10 -cpu=1 -benchtime=5s -benchmem) are set by the perf-gate
// invoker (CI). Each Benchmark function follows the
// plan-mandated warmup discipline: call the benched op exactly once
// after buffer construction and before b.ResetTimer(), so the first
// allocation / branch-predictor / icache warm-up does not steal a
// b.N iteration.
//
// # Buffer construction
//
// Buffers are deterministic (seeded math/rand/v2 ChaCha8) so reruns
// are stable. Per-scan buffers are shaped to keep the SIMD kernel and
// the baseline both touching every byte (the goal is throughput
// equivalence on representative input, not a degenerate "first byte
// matches" or "buffer is empty" timing). Each helper documents its
// density choice and what shape of input it produces.

// benchBufSize is the AC-SIMD-5 hot-path size declared in the plan.
// 64 KiB exercises every SSE2 (16-byte), AVX2 (32-byte), NEON (16-byte),
// and SWAR (8-byte) stride loop comfortably while staying well inside
// L1d on every CI runner we target.
const benchBufSize = 64 * 1024

// benchSeed is the constant PRNG seed used for every deterministic
// buffer in this file. Changing this value invalidates historical
// benchmark numbers — bump it intentionally if you want to switch
// fixtures, and record the bump in commit messages so prior numbers
// remain comparable to themselves but not to the new fixture.
const benchSeed = 0xAC_51_D5_00000005

// newBenchRand returns a deterministic PRNG seeded from benchSeed and
// a per-scan name. Each scan gets its own stream so a future fixture
// change to one scan doesn't perturb the others.
func newBenchRand(name string) *rand.Rand {
	var hash uint64 = benchSeed
	for _, b := range []byte(name) {
		// trivial FNV-style folding — just to spread the seed
		hash ^= uint64(b)
		hash *= 0x100000001b3
	}
	return rand.New(rand.NewChaCha8([32]byte{
		byte(hash), byte(hash >> 8), byte(hash >> 16), byte(hash >> 24),
		byte(hash >> 32), byte(hash >> 40), byte(hash >> 48), byte(hash >> 56),
		// pad to 32 bytes
		1, 2, 3, 4, 5, 6, 7, 8,
		9, 10, 11, 12, 13, 14, 15, 16,
		17, 18, 19, 20, 21, 22, 23, 24,
	}))
}

// makeLocateNewlineBuf returns a 64 KiB buffer that is mostly
// non-newline ASCII with a single '\n' planted at position
// benchBufSize-1. This forces the SIMD kernel to scan the entire
// buffer before finding the match, which is the throughput-bound case
// the AC-SIMD-5 gate cares about. Planting at the end (rather than
// using no-match) keeps the LocateNewline semantics (-1 on absent)
// out of the comparison so the SIMD and baseline both return the
// same int.
func makeLocateNewlineBuf() []byte {
	r := newBenchRand("LocateNewline")
	buf := make([]byte, benchBufSize)
	// fill with non-newline bytes drawn from the printable ASCII
	// range so no incidental '\n' lands in the buffer
	for i := range buf {
		b := byte(r.UintN(95)) + 32 // 32..126 inclusive
		buf[i] = b
	}
	buf[benchBufSize-1] = '\n'
	return buf
}

// makeScanLiteralStringBuf returns a 64 KiB buffer like
// makeLocateNewlineBuf but with a single-quote byte (0x27) planted at
// the end. Same throughput-bound shape.
func makeScanLiteralStringBuf() []byte {
	r := newBenchRand("ScanLiteralString")
	buf := make([]byte, benchBufSize)
	for i := range buf {
		b := byte(r.UintN(94)) + 33 // 33..126 inclusive — skips space
		if b == '\'' {              // and skip the needle
			b = 'x'
		}
		buf[i] = b
	}
	buf[benchBufSize-1] = '\''
	return buf
}

// makeScanBareKeyBuf returns a 64 KiB buffer of bytes drawn entirely
// from the bare-key class [A-Za-z0-9_-]. The SIMD kernel and the naive
// oracle BOTH scan the entire buffer (no break byte) so the
// throughput-bound case is exercised; the returned int is len(buf) in
// both implementations.
func makeScanBareKeyBuf() []byte {
	const class = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"
	r := newBenchRand("ScanBareKey")
	buf := make([]byte, benchBufSize)
	for i := range buf {
		buf[i] = class[r.UintN(uint(len(class)))]
	}
	return buf
}

// makeScanBasicStringBuf returns a 64 KiB buffer of ASCII bytes that
// are NEITHER '"' nor '\\' (the basic-string terminator class). The
// SIMD kernel and the naive oracle both scan the entire buffer; both
// return len(buf).
func makeScanBasicStringBuf() []byte {
	r := newBenchRand("ScanBasicString")
	buf := make([]byte, benchBufSize)
	for i := range buf {
		b := byte(r.UintN(94)) + 33 // 33..126
		if b == '"' || b == '\\' {
			b = 'x'
		}
		buf[i] = b
	}
	return buf
}

// makeScanBasicStringStrictBuf returns a 64 KiB buffer of single-line
// TOML basic-string body bytes that do not require slow-path handling.
// It includes printable ASCII, spaces, and tabs, while excluding quote,
// backslash, DEL, and C0 controls other than tab so both the SIMD kernel
// and the naive oracle scan the whole buffer and return len(buf).
func makeScanBasicStringStrictBuf() []byte {
	r := newBenchRand("ScanBasicStringStrict")
	buf := make([]byte, benchBufSize)
	for i := range buf {
		if r.UintN(16) == 0 {
			buf[i] = '\t'
			continue
		}
		b := byte(r.UintN(95)) + 32 // 32..126
		if b == '"' || b == '\\' {
			b = 'x'
		}
		buf[i] = b
	}
	return buf
}

// makeScanCommentBodyBuf returns a 64 KiB comment body with printable
// ASCII, spaces, tabs, quotes, and hashes, but no LF/CR/DEL/prohibited
// C0 controls. Both the SIMD kernel and the naive oracle scan the full
// buffer and return len(buf).
func makeScanCommentBodyBuf() []byte {
	r := newBenchRand("ScanCommentBody")
	buf := make([]byte, benchBufSize)
	for i := range buf {
		if r.UintN(16) == 0 {
			buf[i] = '\t'
			continue
		}
		buf[i] = byte(r.UintN(95)) + 32 // 32..126
	}
	return buf
}

// makeScanBareValueEndBuf returns a 64 KiB bare-value-like body without
// raw delimiters. It intentionally uses alphanumerics, signs, dots,
// colons, underscores, and quotes so the raw delimiter scan touches every
// byte; parser-level datetime-space joining is not part of this kernel.
func makeScanBareValueEndBuf() []byte {
	const alphabet = `ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+-._:\\"'`
	r := newBenchRand("ScanBareValueEnd")
	buf := make([]byte, benchBufSize)
	for i := range buf {
		buf[i] = alphabet[r.UintN(uint(len(alphabet)))]
	}
	return buf
}

// makeCountLinesBuf returns a 64 KiB line-oriented buffer with one LF
// every 64 bytes. CountLines and the naive oracle both scan every byte;
// the nonzero LF density keeps the benchmark honest for count reduction
// rather than measuring an all-zero mask path only.
func makeCountLinesBuf() []byte {
	buf := make([]byte, benchBufSize)
	for i := range buf {
		if (i+1)%64 == 0 {
			buf[i] = '\n'
		} else {
			buf[i] = 'x'
		}
	}
	return buf
}

// makeSkipWhitespaceBuf returns a 64 KiB buffer of only ' ' (U+0020)
// and '\t' (U+0009) bytes. The SIMD kernel and the naive oracle both
// scan and skip the entire buffer; both return len(buf). Newline is
// intentionally absent — SkipWhitespace's contract treats newline as
// a non-whitespace token-boundary byte.
func makeSkipWhitespaceBuf() []byte {
	r := newBenchRand("SkipWhitespace")
	buf := make([]byte, benchBufSize)
	for i := range buf {
		if r.UintN(2) == 0 {
			buf[i] = ' '
		} else {
			buf[i] = '\t'
		}
	}
	return buf
}

// makeValidateUTF8Buf returns a 64 KiB buffer of valid UTF-8 sequences
// of mixed widths (1/2/3/4-byte). Approximately 50% of the bytes are
// pure ASCII (the SIMD fast path) and 50% are continuation bytes from
// multi-byte sequences (the scalar continuation path). All sequences
// are valid so both the SIMD kernel and stdlib utf8.Valid scan the
// entire buffer; SIMD returns len(buf), the wrapped baseline below
// also returns len(buf).
func makeValidateUTF8Buf() []byte {
	r := newBenchRand("ValidateUTF8")
	buf := make([]byte, 0, benchBufSize+8)
	for len(buf) < benchBufSize {
		// weighted draw: ~70% ASCII, ~15% 2-byte, ~10% 3-byte, ~5% 4-byte
		switch n := r.UintN(100); {
		case n < 70:
			buf = append(buf, byte(0x20+r.UintN(95)))
		case n < 85:
			// U+0080..U+07FF
			cp := 0x80 + r.UintN(0x780)
			buf = utf8.AppendRune(buf, rune(cp))
		case n < 95:
			// U+0800..U+FFFF, skip surrogate range
			cp := 0x800 + r.UintN(0xF800)
			if cp >= 0xD800 && cp <= 0xDFFF {
				cp = 0x4000
			}
			buf = utf8.AppendRune(buf, rune(cp))
		default:
			// U+10000..U+10FFFF
			cp := 0x10000 + r.UintN(0x100000)
			buf = utf8.AppendRune(buf, rune(cp))
		}
	}
	return buf[:benchBufSize]
}

// validateUTF8StdlibBaseline is the AC-SIMD-5 baseline wrapper for
// ValidateUTF8 (per task spec: utf8.Valid returns bool but the gate
// compares int outputs). On valid input it returns len(s) — the same
// value ValidateUTF8 returns. On invalid input it would need to find
// the first-invalid-sequence byte to match ValidateUTF8's semantics,
// but every bench buffer in this file is exclusively valid, so the
// "invalid" branch is never taken in the gate-relevant configuration.
//
// The wrapper is intentionally trivial so the wall-clock cost is
// dominated by utf8.Valid's own scan — that is the apples-to-apples
// comparison AC-SIMD-5 prescribes.
func validateUTF8StdlibBaseline(s []byte) int {
	if utf8.Valid(s) {
		return len(s)
	}
	// Fallback for completeness: linear scan for first invalid sequence
	// using the same DecodeRune logic as naiveValidateUTF8. Not reached
	// from the gate benches.
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

// =====================================================================
// BenchmarkXxx_SIMD — dispatches through the package's funcptr table
// (whatever variant init() bound: AVX2/SSE2/NEON/SWAR per arch + flag).
// =====================================================================

func BenchmarkLocateNewline_SIMD(b *testing.B) {
	buf := makeLocateNewlineBuf()
	b.SetBytes(int64(len(buf)))
	_ = LocateNewline(buf) // warmup (Bench protocol mandate)

	for b.Loop() {
		_ = LocateNewline(buf)
	}
}

func BenchmarkScanLiteralString_SIMD(b *testing.B) {
	buf := makeScanLiteralStringBuf()
	b.SetBytes(int64(len(buf)))
	_ = ScanLiteralString(buf)

	for b.Loop() {
		_ = ScanLiteralString(buf)
	}
}

func BenchmarkScanBareKey_SIMD(b *testing.B) {
	buf := makeScanBareKeyBuf()
	b.SetBytes(int64(len(buf)))
	_ = ScanBareKey(buf)

	for b.Loop() {
		_ = ScanBareKey(buf)
	}
}

func BenchmarkScanBasicString_SIMD(b *testing.B) {
	buf := makeScanBasicStringBuf()
	b.SetBytes(int64(len(buf)))
	_ = ScanBasicString(buf)

	for b.Loop() {
		_ = ScanBasicString(buf)
	}
}

func BenchmarkScanBasicStringStrict_SIMD(b *testing.B) {
	buf := makeScanBasicStringStrictBuf()
	b.SetBytes(int64(len(buf)))
	_ = ScanBasicStringStrict(buf)

	for b.Loop() {
		_ = ScanBasicStringStrict(buf)
	}
}

func BenchmarkScanCommentBody_SIMD(b *testing.B) {
	buf := makeScanCommentBodyBuf()
	b.SetBytes(int64(len(buf)))
	_ = ScanCommentBody(buf)

	for b.Loop() {
		_ = ScanCommentBody(buf)
	}
}

func BenchmarkScanBareValueEnd_SIMD(b *testing.B) {
	buf := makeScanBareValueEndBuf()
	b.SetBytes(int64(len(buf)))
	_ = ScanBareValueEnd(buf)

	for b.Loop() {
		_ = ScanBareValueEnd(buf)
	}
}

func BenchmarkCountLines_SIMD(b *testing.B) {
	buf := makeCountLinesBuf()
	b.SetBytes(int64(len(buf)))
	_ = CountLines(buf)

	for b.Loop() {
		_ = CountLines(buf)
	}
}

func BenchmarkSkipWhitespace_SIMD(b *testing.B) {
	buf := makeSkipWhitespaceBuf()
	b.SetBytes(int64(len(buf)))
	_ = SkipWhitespace(buf)

	for b.Loop() {
		_ = SkipWhitespace(buf)
	}
}

func BenchmarkValidateUTF8_SIMD(b *testing.B) {
	buf := makeValidateUTF8Buf()
	b.SetBytes(int64(len(buf)))
	_ = ValidateUTF8(buf)

	for b.Loop() {
		_ = ValidateUTF8(buf)
	}
}

// =====================================================================
// BenchmarkXxx_Baseline — declared baseline (per AC-SIMD-5 table) on
// the SAME buffer. The SIMD/baseline ratio is the gate input.
// =====================================================================

func BenchmarkLocateNewline_Baseline(b *testing.B) {
	buf := makeLocateNewlineBuf()
	b.SetBytes(int64(len(buf)))
	_ = bytes.IndexByte(buf, '\n')

	for b.Loop() {
		_ = bytes.IndexByte(buf, '\n')
	}
}

func BenchmarkScanLiteralString_Baseline(b *testing.B) {
	buf := makeScanLiteralStringBuf()
	b.SetBytes(int64(len(buf)))
	_ = bytes.IndexByte(buf, '\'')

	for b.Loop() {
		_ = bytes.IndexByte(buf, '\'')
	}
}

func BenchmarkScanBareKey_Baseline(b *testing.B) {
	buf := makeScanBareKeyBuf()
	b.SetBytes(int64(len(buf)))
	_ = naiveScanBareKey(buf)

	for b.Loop() {
		_ = naiveScanBareKey(buf)
	}
}

func BenchmarkScanBasicString_Baseline(b *testing.B) {
	buf := makeScanBasicStringBuf()
	b.SetBytes(int64(len(buf)))
	_ = naiveScanBasicString(buf)

	for b.Loop() {
		_ = naiveScanBasicString(buf)
	}
}

func BenchmarkScanBasicStringStrict_Baseline(b *testing.B) {
	buf := makeScanBasicStringStrictBuf()
	b.SetBytes(int64(len(buf)))
	_ = naiveScanBasicStringStrict(buf)

	for b.Loop() {
		_ = naiveScanBasicStringStrict(buf)
	}
}

func BenchmarkScanCommentBody_Baseline(b *testing.B) {
	buf := makeScanCommentBodyBuf()
	b.SetBytes(int64(len(buf)))
	_ = naiveScanCommentBody(buf)

	for b.Loop() {
		_ = naiveScanCommentBody(buf)
	}
}

func BenchmarkScanBareValueEnd_Baseline(b *testing.B) {
	buf := makeScanBareValueEndBuf()
	b.SetBytes(int64(len(buf)))
	_ = naiveScanBareValueEnd(buf)

	for b.Loop() {
		_ = naiveScanBareValueEnd(buf)
	}
}

func BenchmarkCountLines_Baseline(b *testing.B) {
	buf := makeCountLinesBuf()
	b.SetBytes(int64(len(buf)))
	_ = naiveCountLines(buf)

	for b.Loop() {
		_ = naiveCountLines(buf)
	}
}

func BenchmarkSkipWhitespace_Baseline(b *testing.B) {
	buf := makeSkipWhitespaceBuf()
	b.SetBytes(int64(len(buf)))
	_ = naiveSkipWhitespace(buf)

	for b.Loop() {
		_ = naiveSkipWhitespace(buf)
	}
}

func BenchmarkValidateUTF8_Baseline(b *testing.B) {
	buf := makeValidateUTF8Buf()
	b.SetBytes(int64(len(buf)))
	_ = validateUTF8StdlibBaseline(buf)

	for b.Loop() {
		_ = validateUTF8StdlibBaseline(buf)
	}
}

// BenchmarkScanSmallSizes is intentionally informational: toml-perf-gate
// selects only the top-level Benchmark<Scan>_(SIMD|Baseline) rows. These
// sub-benchmarks expose small-n call/tail costs without weakening the 64K
// hard gate row selection.
func BenchmarkScanSmallSizes(b *testing.B) {
	type scanBench struct {
		name string
		buf  func(int) []byte
		fn   func([]byte) int
	}
	benches := []scanBench{
		{name: "ScanBasicStringStrict", buf: func(n int) []byte { return makeSmallRepeated(n, 'x') }, fn: ScanBasicStringStrict},
		{name: "ScanCommentBody", buf: func(n int) []byte { return makeSmallRepeated(n, 'x') }, fn: ScanCommentBody},
		{name: "ScanBareValueEnd", buf: func(n int) []byte { return makeSmallRepeated(n, 'x') }, fn: ScanBareValueEnd},
		{name: "CountLines", buf: makeSmallLineBuf, fn: CountLines},
	}
	for _, bm := range benches {
		for _, size := range []int{0, 1, 7, 8, 15, 16, 31, 32, 64, 256, 1024} {
			b.Run(bm.name+"/n="+strconv.Itoa(size), func(b *testing.B) {
				buf := bm.buf(size)
				b.SetBytes(int64(len(buf)))
				_ = bm.fn(buf)
				for b.Loop() {
					_ = bm.fn(buf)
				}
			})
		}
	}
}

func makeSmallRepeated(n int, b byte) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = b
	}
	return buf
}

func makeSmallLineBuf(n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		if (i+1)%16 == 0 {
			buf[i] = '\n'
		} else {
			buf[i] = 'x'
		}
	}
	return buf
}
