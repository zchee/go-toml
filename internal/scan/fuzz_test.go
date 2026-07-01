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
	"slices"
	"strings"
	"testing"
)

// fuzz_test.go ships one fuzz target per exported scan kernel, closing
// AC-SIMD-6 (correctness oracle including fuzz) for Phase 1.
//
// Each target compares the dispatched implementation (whatever variant
// init() bound on the current arch + GOEXPERIMENT) against the naive
// oracle from naive_scan_test.go on every fuzzed input. If they ever
// disagree, the fuzz tool surfaces the offending input and persists it
// under testdata/fuzz/Fuzz<ScanName>/ as a regression seed for the next
// PR — the same mechanism Go's stdlib fuzzers use.
//
// Seed strategy: bulk seeds are added inline via f.Add (Go fuzz
// recommended pattern) so the corpus is reproducible from source and
// not lost to a stale checkout. One canonical "disk" seed lives under
// testdata/fuzz/Fuzz<ScanName>/ per scan to satisfy the task spec's
// directory-existence requirement and as a placeholder for crash
// minimization output the fuzz tool writes there automatically.
//
// Seed cardinality per scan: ~280 inputs (256 single bytes + ~20
// boundary cases + a handful of pathological inputs). ValidateUTF8 gets
// ~15 extra UTF-8-specific seeds (BOM, overlong, surrogate halves,
// truncated multi-byte, valid 2/3/4-byte). Combined with Go's
// coverage-guided exploration this gives the per-scan FuzzX target a
// sound launch pad for `-fuzztime=60s` CI runs.

// addBoundarySeeds populates f with the byte-stride boundary inputs
// every scan kernel cares about: lengths around the 8-byte SWAR,
// 16-byte SSE2/NEON, and 32-byte AVX2 strides, in two shapes — pure
// classByte runs and class runs with a single terminator planted at
// each boundary-relevant position.
//
// classByte is a byte the scan would "consume" or "skip" (e.g. 'a' for
// ScanBareKey, ' ' for SkipWhitespace). terminator is a byte that
// breaks the scan (e.g. '.' for ScanBareKey, '"' for ScanBasicString).
// LocateNewline reverses the convention — its classByte is any non-'\n'
// byte and terminator is '\n'.
func addBoundarySeeds(f *testing.F, classByte, terminator byte) {
	// Empty input.
	f.Add([]byte(""))
	// Single byte of every possible value (256 seeds).
	for v := range 256 {
		f.Add([]byte{byte(v)})
	}
	// Pure-classByte runs at every boundary-relevant length.
	lengths := []int{7, 8, 9, 15, 16, 17, 31, 32, 33, 64, 128, 1024}
	for _, n := range lengths {
		all := []byte(strings.Repeat(string(classByte), n))
		f.Add(slices.Clone(all))
		// A single terminator planted at each boundary position.
		positions := []int{0, 1, 7, 8, 9, 15, 16, 17, 31, n / 2, n - 1}
		for _, pos := range positions {
			if pos < 0 || pos >= n {
				continue
			}
			b := slices.Clone(all)
			b[pos] = terminator
			f.Add(b)
		}
	}
}

// addUTF8Seeds adds the UTF-8-specific tricky inputs the task spec
// enumerates: BOM, overlong, surrogate halves, truncated multi-byte
// sequences, valid 2/3/4-byte sequences, mixed-width strings, and
// invalid bytes at exact SIMD stride boundaries.
func addUTF8Seeds(f *testing.F) {
	// UTF-8 BOM followed by ASCII.
	f.Add([]byte{0xEF, 0xBB, 0xBF, 'h', 'i'})
	// Overlong encoding of NUL (invalid).
	f.Add([]byte{0xC0, 0x80})
	// High surrogate (invalid in UTF-8).
	f.Add([]byte{0xED, 0xA0, 0x80})
	// Low surrogate (invalid in UTF-8).
	f.Add([]byte{0xED, 0xBF, 0xBF})
	// Truncated 2-byte sequence.
	f.Add([]byte{0xC3})
	// Truncated 3-byte sequence.
	f.Add([]byte{0xE2, 0x82})
	// Truncated 4-byte sequence.
	f.Add([]byte{0xF0, 0x9D, 0x84})
	// Valid 2-byte sequence (é).
	f.Add([]byte{0xC3, 0xA9})
	// Valid 3-byte sequence (€).
	f.Add([]byte{0xE2, 0x82, 0xAC})
	// Valid 4-byte sequence (𝄞).
	f.Add([]byte{0xF0, 0x9D, 0x84, 0x9E})
	// Mixed-width sequence.
	f.Add([]byte("hello 世界 ✓ é"))
	// Invalid high-bit byte at the 16-byte (SSE2/NEON) boundary.
	b16 := make([]byte, 16)
	for i := range b16 {
		b16[i] = 'a'
	}
	b16[15] = 0x80
	f.Add(b16)
	// Invalid high-bit byte at the 32-byte (AVX2) boundary.
	b32 := make([]byte, 32)
	for i := range b32 {
		b32[i] = 'a'
	}
	b32[31] = 0x80
	f.Add(b32)
	// Invalid high-bit byte just past the 32-byte boundary.
	b33 := make([]byte, 33)
	for i := range b33 {
		b33[i] = 'a'
	}
	b33[32] = 0x80
	f.Add(b33)
	// Long ASCII run then a single invalid byte (exercises the ASCII
	// fast path + scalar continuation transition).
	long := append([]byte(strings.Repeat("x", 1023)), 0x80)
	f.Add(long)
}

// =====================================================================
// Per-scan fuzz targets. Each asserts the dispatched scan equals the
// naive oracle on every fuzzed input.
// =====================================================================

// FuzzScanBareKey explores inputs that exercise the ScanBareKey kernel
// (bare-key class [A-Za-z0-9_-]). Seeds include all 256 single-byte
// values plus boundary-length pure-class runs broken by '.' at each
// stride-relevant position.
func FuzzScanBareKey(f *testing.F) {
	addBoundarySeeds(f, 'a', '.')
	f.Fuzz(func(t *testing.T, data []byte) {
		got := ScanBareKey(data)
		want := naiveScanBareKey(data)
		if got != want {
			t.Errorf("ScanBareKey(%x) = %d, want %d", data, got, want)
		}
	})
}

// FuzzScanBasicString explores inputs for the basic-string terminator
// scan. Terminator class is {'"', '\\'} — addBoundarySeeds plants one
// of them ('"') at boundary positions; coverage-guided fuzz finds
// the '\\' case from the dispatcher's behavior on single-byte 92.
func FuzzScanBasicString(f *testing.F) {
	addBoundarySeeds(f, 'x', '"')
	f.Fuzz(func(t *testing.T, data []byte) {
		got := ScanBasicString(data)
		want := naiveScanBasicString(data)
		if got != want {
			t.Errorf("ScanBasicString(%x) = %d, want %d", data, got, want)
		}
	})
}

// FuzzScanBasicStringStrict explores inputs for the fused single-line
// basic-string scan. It shares the quote/backslash terminator coverage
// with FuzzScanBasicString and adds explicit boundary controls for the
// TOML C0-control/DEL slow path while preserving tab as body text.
func FuzzScanBasicStringStrict(f *testing.F) {
	addBoundarySeeds(f, 'x', '"')
	for _, terminator := range []byte{'\\', '\n', '\r', 0x00, 0x1f, 0x7f} {
		for _, n := range []int{8, 16, 32, 64} {
			b := []byte(strings.Repeat("x", n))
			b[n-1] = terminator
			f.Add(b)
		}
	}
	f.Add([]byte("tab\tis\tbody"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got := ScanBasicStringStrict(data)
		want := naiveScanBasicStringStrict(data)
		if got != want {
			t.Errorf("ScanBasicStringStrict(%x) = %d, want %d", data, got, want)
		}
	})
}

// FuzzScanCommentBody explores inputs for the comment-body scan.
// The scan stops at LF/CR as line terminators and at DEL/C0 controls
// other than tab; quote, backslash, and hash are ordinary comment body
// bytes once the leading '#' has already been consumed by the parser.
func FuzzScanCommentBody(f *testing.F) {
	addBoundarySeeds(f, 'x', '\n')
	for _, terminator := range []byte{'\r', 0x00, 0x1f, 0x7f} {
		for _, n := range []int{8, 16, 32, 64} {
			b := []byte(strings.Repeat("x", n))
			b[n-1] = terminator
			f.Add(b)
		}
	}
	f.Add([]byte("quote \" and hash # are body"))
	f.Add([]byte("tab\tis\tbody"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got := ScanCommentBody(data)
		want := naiveScanCommentBody(data)
		if got != want {
			t.Errorf("ScanCommentBody(%x) = %d, want %d", data, got, want)
		}
	})
}

// FuzzScanBareValueEnd explores inputs for the raw bare-value delimiter
// scan. Datetime values containing one grammar-space are handled by the
// parser-level scanBareValueEnd helper, not by this raw delimiter kernel.
func FuzzScanBareValueEnd(f *testing.F) {
	addBoundarySeeds(f, 'x', ' ')
	for _, terminator := range []byte{'\t', '\r', '\n', ',', ']', '}', '#', '='} {
		for _, n := range []int{8, 16, 32, 64} {
			b := []byte(strings.Repeat("x", n))
			b[n-1] = terminator
			f.Add(b)
		}
	}
	f.Add([]byte("1979-05-27T07:32:00Z"))
	f.Add([]byte("1979-05-27 07:32:00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		got := ScanBareValueEnd(data)
		want := naiveScanBareValueEnd(data)
		if got != want {
			t.Errorf("ScanBareValueEnd(%x) = %d, want %d", data, got, want)
		}
	})
}

// FuzzCountLines explores LF counting. CR is intentionally just another
// byte here; parser line/column semantics decide how to display CRLF and
// lone-CR positions on top of this LF-only count.
func FuzzCountLines(f *testing.F) {
	addBoundarySeeds(f, 'x', '\n')
	f.Add([]byte("a\r\nb\rc\n"))
	f.Add([]byte(strings.Repeat("line\n", 128)))
	f.Fuzz(func(t *testing.T, data []byte) {
		got := CountLines(data)
		want := naiveCountLines(data)
		if got != want {
			t.Errorf("CountLines(%x) = %d, want %d", data, got, want)
		}
	})
}

// FuzzScanLiteralString explores inputs for the literal-string
// terminator scan (single-quote 0x27). Terminator class is single-byte
// so the seeding shape matches FuzzScanBareKey exactly with a 0x27
// terminator instead of '.'.
func FuzzScanLiteralString(f *testing.F) {
	addBoundarySeeds(f, 'x', '\'')
	f.Fuzz(func(t *testing.T, data []byte) {
		got := ScanLiteralString(data)
		want := naiveScanLiteralString(data)
		if got != want {
			t.Errorf("ScanLiteralString(%x) = %d, want %d", data, got, want)
		}
	})
}

// FuzzSkipWhitespace explores inputs for the whitespace-prefix scan.
// classByte is ' '; terminator is 'x' (any non-whitespace, non-tab).
// '\n' and '\t' are intentionally excluded from "terminator" — they
// have their own roles in the SkipWhitespace contract (\t is also
// whitespace; \n is a token boundary the LocateNewline scan owns).
func FuzzSkipWhitespace(f *testing.F) {
	addBoundarySeeds(f, ' ', 'x')
	// Add '\t' as the additional class member; addBoundarySeeds only
	// uses one classByte but SkipWhitespace's class is two bytes
	// ({' ', '\t'}). A few extra seeds with mixed ' '/'\t' runs cover
	// the under-represented case.
	for _, n := range []int{8, 16, 32, 64} {
		mixed := make([]byte, n)
		for i := range mixed {
			if i%2 == 0 {
				mixed[i] = ' '
			} else {
				mixed[i] = '\t'
			}
		}
		f.Add(mixed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		got := SkipWhitespace(data)
		want := naiveSkipWhitespace(data)
		if got != want {
			t.Errorf("SkipWhitespace(%x) = %d, want %d", data, got, want)
		}
	})
}

// FuzzLocateNewline explores inputs for the newline-search scan.
// Convention is reversed: classByte is 'x' (the "no-match" byte that
// the kernel scans past) and terminator is '\n' (the byte the kernel
// is searching for). The seeding still hits every boundary position.
//
// Important contract: LocateNewline returns -1 on absent newline, not
// len(s). The naive oracle naiveLocateNewline returns -1 too. The
// fuzz target's equality check covers both the present and absent
// cases without special-casing.
func FuzzLocateNewline(f *testing.F) {
	addBoundarySeeds(f, 'x', '\n')
	f.Fuzz(func(t *testing.T, data []byte) {
		got := LocateNewline(data)
		want := naiveLocateNewline(data)
		if got != want {
			t.Errorf("LocateNewline(%x) = %d, want %d", data, got, want)
		}
	})
}

// FuzzValidateUTF8 explores inputs for the UTF-8 validation scan,
// the most state-rich kernel. Seeds include the standard boundary
// set plus the UTF-8-specific tricky inputs (BOM, overlong, surrogate
// halves, truncated multi-byte, valid 2/3/4-byte at stride
// boundaries).
//
// Note: addBoundarySeeds plants 0x80 as the terminator here so the
// fuzz tool has direct coverage of the ASCII-fast-path → scalar
// continuation transition at every boundary length.
func FuzzValidateUTF8(f *testing.F) {
	addBoundarySeeds(f, 'a', 0x80)
	addUTF8Seeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		got := ValidateUTF8(data)
		want := naiveValidateUTF8(data)
		if got != want {
			t.Errorf("ValidateUTF8(%x) = %d, want %d", data, got, want)
		}
	})
}
