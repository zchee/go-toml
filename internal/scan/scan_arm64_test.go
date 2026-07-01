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

import (
	"strings"
	"testing"
)

// scan_arm64_test.go is the per-variant correctness gate for the arm64
// NEON backend. There is only one variant per scan (NEON is
// ABI-guaranteed on arm64, so no fallback path) so every test runs
// unconditionally — no host feature-detect gate is needed.
//
// Two complementary test classes cover correctness, in the same shape
// as scan_amd64_test.go:
//   - Golden table tests on hand-curated boundary cases (16-byte stride
//     boundaries ± 1, plus the tail handler boundaries).
//   - 10 K seeded-PRNG property cases per scan against the naive oracle.
//     Lower than property_test.go's 100 K because property_test.go
//     already runs full coverage against the dispatched func; this file
//     pins the per-variant NEON entry point.

const arm64PropertyCases = 10_000

type arm64Variant struct {
	name   string
	impl   func([]byte) int
	oracle func([]byte) int
}

func arm64Variants() []arm64Variant {
	return []arm64Variant{
		{"NEON_ScanBareKey", scanBareKeyNEON, naiveScanBareKey},
		{"NEON_ScanBasicString", scanBasicStringNEON, naiveScanBasicString},
		{"NEON_ScanBasicStringStrict", scanBasicStringStrictNEON, naiveScanBasicStringStrict},
		{"NEON_ScanCommentBody", scanCommentBodyNEON, naiveScanCommentBody},
		{"NEON_ScanBareValueEnd", scanBareValueEndNEON, naiveScanBareValueEnd},
		{"NEON_CountLines", countLinesNEON, naiveCountLines},
		{"NEON_ScanLiteralString", scanLiteralStringNEON, naiveScanLiteralString},
		{"NEON_SkipWhitespace", skipWhitespaceNEON, naiveSkipWhitespace},
		{"NEON_LocateNewline", locateNewlineNEON, naiveLocateNewline},
		{"NEON_ValidateUTF8", validateUTF8NEON, naiveValidateUTF8},
	}
}

// arm64GoldenCase mirrors the goldenCase shape used by scan_amd64_test.go.
// It is duplicated here because that type lives behind the
// goexperiment.simd && amd64 build tag and is not visible on arm64.
type arm64GoldenCase struct {
	name  string
	input []byte
	want  map[string]int // keyed by scan name (e.g. "ScanBareKey")
}

// arm64ScanNameFromVariant turns "NEON_ScanBareKey" into "ScanBareKey".
func arm64ScanNameFromVariant(v string) string {
	if _, after, ok := strings.Cut(v, "_"); ok {
		return after
	}
	return v
}

// arm64Golden concentrates boundary cases on the 16-byte NEON stride
// (± 1) plus inputs that exercise the per-byte tail handler after the
// vectorized main loop drains.
var arm64Golden = []arm64GoldenCase{
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
		name:  "len_1_all_class",
		input: []byte("a"),
		want: map[string]int{
			"ScanBareKey":       1,
			"ScanBasicString":   1,
			"ScanLiteralString": 1,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      1,
		},
	},
	{
		name:  "len_15_all_class",
		input: []byte(strings.Repeat("a", 15)),
		want: map[string]int{
			"ScanBareKey":       15,
			"ScanBasicString":   15,
			"ScanLiteralString": 15,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      15,
		},
	},
	{
		name:  "len_16_all_class",
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
		name:  "len_17_all_class",
		input: []byte(strings.Repeat("a", 17)),
		want: map[string]int{
			"ScanBareKey":       17,
			"ScanBasicString":   17,
			"ScanLiteralString": 17,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      17,
		},
	},
	{
		name:  "break_at_byte_0",
		input: []byte(".aaaaaaaaaaaaaaaa"),
		want: map[string]int{
			"ScanBareKey":       0,
			"ScanBasicString":   17,
			"ScanLiteralString": 17,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      17,
		},
	},
	{
		name:  "break_at_byte_15",
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
		name:  "break_at_byte_16",
		input: []byte(strings.Repeat("a", 16) + ".rest"),
		want: map[string]int{
			"ScanBareKey":       16,
			"ScanBasicString":   21,
			"ScanLiteralString": 21,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      21,
		},
	},
	{
		name:  "break_at_byte_17",
		input: []byte(strings.Repeat("a", 17) + ".rest"),
		want: map[string]int{
			"ScanBareKey":       17,
			"ScanBasicString":   22,
			"ScanLiteralString": 22,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      22,
		},
	},
	{
		name:  "quote_at_15",
		input: []byte(strings.Repeat("a", 15) + "\"rest"),
		want: map[string]int{
			"ScanBareKey":       15,
			"ScanBasicString":   15,
			"ScanLiteralString": 20,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      20,
		},
	},
	{
		name:  "quote_at_16",
		input: []byte(strings.Repeat("a", 16) + "\"rest"),
		want: map[string]int{
			"ScanBareKey":       16,
			"ScanBasicString":   16,
			"ScanLiteralString": 21,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      21,
		},
	},
	{
		name:  "newline_at_15",
		input: []byte(strings.Repeat("a", 15) + "\n"),
		want: map[string]int{
			"ScanBareKey":       15,
			"ScanBasicString":   16,
			"ScanLiteralString": 16,
			"SkipWhitespace":    0,
			"LocateNewline":     15,
			"ValidateUTF8":      16,
		},
	},
	{
		name:  "newline_at_16",
		input: []byte(strings.Repeat("a", 16) + "\n"),
		want: map[string]int{
			"ScanBareKey":       16,
			"ScanBasicString":   17,
			"ScanLiteralString": 17,
			"SkipWhitespace":    0,
			"LocateNewline":     16,
			"ValidateUTF8":      17,
		},
	},
	{
		name:  "newline_only_in_tail",
		input: []byte(strings.Repeat("a", 18) + "\n"),
		want: map[string]int{
			"ScanBareKey":       18,
			"ScanBasicString":   19,
			"ScanLiteralString": 19,
			"SkipWhitespace":    0,
			"LocateNewline":     18,
			"ValidateUTF8":      19,
		},
	},
	{
		name:  "all_whitespace_24",
		input: []byte(strings.Repeat(" ", 24) + "x"),
		want: map[string]int{
			"ScanBareKey":       0,
			"ScanBasicString":   25,
			"ScanLiteralString": 25,
			"SkipWhitespace":    24,
			"LocateNewline":     -1,
			"ValidateUTF8":      25,
		},
	},
	{
		name:  "high_bit_at_16",
		input: append([]byte(strings.Repeat("a", 16)), 0x80),
		want: map[string]int{
			"ScanBareKey":       16,
			"ScanBasicString":   17,
			"ScanLiteralString": 17,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      16,
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
	{
		name:  "utf8_invalid_at_16",
		input: append([]byte(strings.Repeat("x", 16)), 0x80),
		want: map[string]int{
			"ScanBareKey":       16,
			"ScanBasicString":   17,
			"ScanLiteralString": 17,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      16,
		},
	},
}

func TestARM64Variants_Golden(t *testing.T) {
	t.Parallel()
	for _, v := range arm64Variants() {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			scan := arm64ScanNameFromVariant(v.name)
			for _, g := range arm64Golden {
				want, ok := g.want[scan]
				if !ok {
					switch scan {
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

func TestARM64Variants_Property(t *testing.T) {
	t.Parallel()
	seed := loadPropertySeed(t)
	for _, v := range arm64Variants() {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			r := newPropertyRand(seed, v.name)
			buf := make([]byte, propertyMaxLen)
			for n := range arm64PropertyCases {
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
