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
	"simd/archsimd"
	"strings"
	"testing"
)

// scan_amd64_test.go is the per-variant correctness gate for the amd64
// backends. For every scan kernel, both the SSE2 and AVX2 variants are
// tested against the naive oracle from naive_scan_test.go. AVX2 cases
// skip on hosts where archsimd.X86.AVX2() returns false; SSE2 cases run
// unconditionally because the amd64 Go ABI guarantees SSE2.
//
// Two complementary test classes cover correctness:
//   - Golden table tests on hand-curated boundary cases (stride
//     boundaries at 16/32-byte multiples ± 1).
//   - 10 K seeded-PRNG property cases per (variant, scan) drawing from
//     the same testdata/property_seed.txt as the T1 dispatched
//     property test. The case count is intentionally lower than T1's
//     100 K because we have 12 (variant, scan) pairs here — 12 × 100 K
//     would be excessive on every CI run.

const amd64PropertyCases = 10_000

type amd64Variant struct {
	name    string
	impl    func([]byte) int
	oracle  func([]byte) int
	wantAVX bool // true => skip on !AVX2 hosts
}

func amd64Variants() []amd64Variant {
	return []amd64Variant{
		{"SSE2_ScanBareKey", scanBareKeySSE2, naiveScanBareKey, false},
		{"SSE2_ScanBasicString", scanBasicStringSSE2, naiveScanBasicString, false},
		{"SSE2_ScanBasicStringEscape", scanBasicStringEscapeSSE2, naiveScanBasicStringEscape, false},
		{"SSE2_ScanBasicStringStrict", scanBasicStringStrictSSE2, naiveScanBasicStringStrict, false},
		{"SSE2_ScanCommentBody", scanCommentBodySSE2, naiveScanCommentBody, false},
		{"SSE2_ScanBareValueEnd", scanBareValueEndSSE2, naiveScanBareValueEnd, false},
		{"SSE2_CountLines", countLinesSSE2, naiveCountLines, false},
		{"SSE2_ScanLiteralString", scanLiteralStringSSE2, naiveScanLiteralString, false},
		{"SSE2_SkipWhitespace", skipWhitespaceSSE2, naiveSkipWhitespace, false},
		{"SSE2_LocateNewline", locateNewlineSSE2, naiveLocateNewline, false},
		{"SSE2_ValidateUTF8", validateUTF8SSE2, naiveValidateUTF8, false},
		{"AVX2_ScanBareKey", scanBareKeyAVX2, naiveScanBareKey, true},
		{"AVX2_ScanBasicString", scanBasicStringAVX2, naiveScanBasicString, true},
		{"AVX2_ScanBasicStringEscape", scanBasicStringEscapeAVX2, naiveScanBasicStringEscape, true},
		{"AVX2_ScanBasicStringStrict", scanBasicStringStrictAVX2, naiveScanBasicStringStrict, true},
		{"AVX2_ScanCommentBody", scanCommentBodyAVX2, naiveScanCommentBody, true},
		{"AVX2_ScanBareValueEnd", scanBareValueEndAVX2, naiveScanBareValueEnd, true},
		{"AVX2_CountLines", countLinesAVX2, naiveCountLines, true},
		{"AVX2_ScanLiteralString", scanLiteralStringAVX2, naiveScanLiteralString, true},
		{"AVX2_SkipWhitespace", skipWhitespaceAVX2, naiveSkipWhitespace, true},
		{"AVX2_LocateNewline", locateNewlineAVX2, naiveLocateNewline, true},
		{"AVX2_ValidateUTF8", validateUTF8AVX2, naiveValidateUTF8, true},
	}
}

// amd64GoldenCases concentrates boundary cases on the 16-byte and
// 32-byte stride boundaries that SSE2 and AVX2 cross, plus inputs that
// exercise the scalar tail after a SIMD stride loop. Each case must
// produce the same int from every variant — table driven across the
// twelve (variant, scan) pairs above.
type goldenCase struct {
	name  string
	input []byte
	want  map[string]int // keyed by scan name (e.g. "ScanBareKey")
}

var amd64Golden = []goldenCase{
	{
		name:  "empty",
		input: []byte(""),
		want: map[string]int{
			"ScanBareKey":       0,
			"ScanBasicString":   0,
			"ScanLiteralString": 0,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      0,
		},
	},
	{
		name:  "all_ascii_16",
		input: []byte(strings.Repeat("a", 16)),
		want: map[string]int{
			"ScanBareKey":       16,
			"ScanBasicString":   16,
			"ScanLiteralString": 16,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      16,
		},
	},
	{
		name:  "all_ascii_32",
		input: []byte(strings.Repeat("a", 32)),
		want: map[string]int{
			"ScanBareKey":       32,
			"ScanBasicString":   32,
			"ScanLiteralString": 32,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      32,
		},
	},
	{
		name:  "all_ascii_33",
		input: []byte(strings.Repeat("a", 33)),
		want: map[string]int{
			"ScanBareKey":       33,
			"ScanBasicString":   33,
			"ScanLiteralString": 33,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      33,
		},
	},
	{
		name:  "break_at_15",
		input: []byte(strings.Repeat("a", 15) + ".rest"),
		want: map[string]int{
			"ScanBareKey":       15,
			"ScanBasicString":   20,
			"ScanLiteralString": 20,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      20,
		},
	},
	{
		name:  "break_at_31",
		input: []byte(strings.Repeat("a", 31) + ".rest"),
		want: map[string]int{
			"ScanBareKey":       31,
			"ScanBasicString":   36,
			"ScanLiteralString": 36,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      36,
		},
	},
	{
		name:  "quote_at_16",
		input: []byte(strings.Repeat("a", 16) + "\""),
		want: map[string]int{
			"ScanBareKey":       16,
			"ScanBasicString":   16,
			"ScanLiteralString": 17,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      17,
		},
	},
	{
		name:  "newline_at_32",
		input: []byte(strings.Repeat("a", 32) + "\n"),
		want: map[string]int{
			"ScanBareKey":       32,
			"ScanBasicString":   33,
			"ScanLiteralString": 33,
			"SkipWhitespace":    0,
			"LocateNewline":     32,
			"ValidateUTF8":      33,
		},
	},
	{
		name:  "long_whitespace_then_x",
		input: []byte(strings.Repeat(" ", 40) + "x"),
		want: map[string]int{
			"ScanBareKey":       0,
			"ScanBasicString":   41,
			"ScanLiteralString": 41,
			"SkipWhitespace":    40,
			"LocateNewline":     -1,
			"ValidateUTF8":      41,
		},
	},
	{
		name:  "high_bit_at_32",
		input: append([]byte(strings.Repeat("a", 32)), 0x80),
		want: map[string]int{
			"ScanBareKey":       32,
			"ScanBasicString":   33,
			"ScanLiteralString": 33,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      32,
		},
	},
	{
		name:  "utf8_two_byte_after_ascii_run",
		input: append(append([]byte(strings.Repeat("x", 30)), 0xC3, 0xA9), 'z'),
		want: map[string]int{
			"ScanBareKey":       30,
			"ScanBasicString":   33,
			"ScanLiteralString": 33,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      33,
		},
	},
}

func scanNameFromVariant(v string) string {
	// "SSE2_ScanBareKey" -> "ScanBareKey"; same for AVX2_.
	if idx := strings.IndexByte(v, '_'); idx >= 0 {
		return v[idx+1:]
	}
	return v
}

func TestAMD64Variants_Golden(t *testing.T) {
	t.Parallel()
	for _, v := range amd64Variants() {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			if v.wantAVX && !archsimd.X86.AVX2() {
				t.Skipf("%s requires AVX2; host lacks it", v.name)
			}
			scan := scanNameFromVariant(v.name)
			for _, g := range amd64Golden {
				want, ok := g.want[scan]
				if !ok {
					switch scan {
					case "ScanBasicStringEscape":
						want = naiveScanBasicStringEscape(g.input)
					case "ScanBasicStringStrict":
						want = naiveScanBasicStringStrict(g.input)
					case "ScanCommentBody":
						want = naiveScanCommentBody(g.input)
					case "ScanBareValueEnd":
						want = naiveScanBareValueEnd(g.input)
					case "CountLines":
						want = naiveCountLines(g.input)
					default:
						t.Fatalf("golden case %q missing want for scan %q", g.name, scan)
					}
				}
				got := v.impl(g.input)
				if got != want {
					t.Errorf("[%s] %s(%q) = %d, want %d",
						v.name, scan, g.input, got, want)
				}
			}
		})
	}
}

func TestAMD64Variants_Property(t *testing.T) {
	t.Parallel()
	seed := loadPropertySeed(t)
	for _, v := range amd64Variants() {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			if v.wantAVX && !archsimd.X86.AVX2() {
				t.Skipf("%s requires AVX2; host lacks it", v.name)
			}
			r := newPropertyRand(seed, v.name)
			buf := make([]byte, propertyMaxLen)
			for n := 0; n < amd64PropertyCases; n++ {
				l := r.IntN(propertyMaxLen + 1)
				for i := 0; i < l; {
					w := r.Uint64()
					for k := 0; k < 8 && i < l; k, i = k+1, i+1 {
						buf[i] = byte(w >> (8 * k))
					}
				}
				got := v.impl(buf[:l])
				want := v.oracle(buf[:l])
				if got != want {
					t.Fatalf("[%s] case %d len=%d: got=%d want=%d input=%x",
						v.name, n, l, got, want, buf[:l])
				}
			}
		})
	}
}
