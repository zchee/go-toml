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

import "testing"

// dispatch_test.go owns the shared infrastructure for AC-SIMD-7's
// cross-backend equivalence assertion. It has no build tag so it is
// compiled in every (arch, GOEXPERIMENT, force_swar) configuration; the
// per-arch dispatch_{amd64,arm64,swar}_test.go files reference these
// helpers to force each available variant through the exported API
// (ScanBareKey, …) and assert it agrees with the naive oracle on a
// shared smoke suite.
//
// Variant-forcing is intentionally split across per-arch files because
// the per-variant symbols (scanBareKeySSE2, scanBareKeyAVX2,
// scanBareKeyNEON, scanBareKeySWAR) only exist under their own backend
// file's build tag — references from an untagged file would fail to
// compile on the wrong arch.

// dispatchSmokeCase pairs an input slice with the expected scan results
// keyed by exported scan name. It is consumed by runDispatchSmoke,
// which is called from each per-arch dispatch test after a swapDispatch
// has pinned the variant under test.
type dispatchSmokeCase struct {
	name  string
	input []byte
	want  map[string]int
}

// dispatchSmokeCases is the shared correctness fixture used by every
// per-variant dispatch test. The cases concentrate on inputs that
// exercise:
//
//   - 16-byte SSE2 stride boundaries and their immediate neighbors
//     (15/16/17) so the SSE2 main loop vs. scalar-tail transition is hit;
//   - 32-byte AVX2 stride boundaries (31/32/33) so the AVX2 main loop
//     and its <32-byte SSE2 tail fall-through are both exercised;
//   - inputs with each kernel's "interesting byte" (quote, backslash,
//     single-quote, space/tab, newline, high-bit byte) placed at and
//     across those boundaries;
//   - the empty input as the trivial-length edge case;
//   - one valid multi-byte UTF-8 sequence so ValidateUTF8's scalar
//     continuation is exercised through the dispatch.
//
// Coverage intentionally stays focused — exhaustive per-variant testing
// already lives in scan_{amd64,arm64}_test.go and property_test.go.
// dispatch_test.go's contribution is to prove the dispatcher itself
// reaches each variant under test forcing.
var dispatchSmokeCases = []dispatchSmokeCase{
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
		name:  "small_bare_key",
		input: []byte("foo_bar-123"),
		want: map[string]int{
			"ScanBareKey":       11,
			"ScanBasicString":   11,
			"ScanLiteralString": 11,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      11,
		},
	},
	{
		name:  "16byte_all_class",
		input: []byte("aaaaaaaaaaaaaaaa"),
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
		name:  "32byte_all_class",
		input: []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
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
		name:  "33byte_all_class",
		input: []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
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
		name:  "quote_at_16",
		input: []byte("aaaaaaaaaaaaaaaa\"rest"),
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
		name:  "literal_quote_at_15",
		input: []byte("aaaaaaaaaaaaaaa'rest"),
		want: map[string]int{
			"ScanBareKey":       15,
			"ScanBasicString":   20,
			"ScanLiteralString": 15,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      20,
		},
	},
	{
		name:  "backslash_in_basic_string",
		input: []byte("hello\\nworld"),
		want: map[string]int{
			"ScanBareKey":       5,
			"ScanBasicString":   5,
			"ScanLiteralString": 12,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      12,
		},
	},
	{
		name:  "ws_then_x",
		input: []byte("    \t\tfoo"),
		want: map[string]int{
			"ScanBareKey":       0,
			"ScanBasicString":   9,
			"ScanLiteralString": 9,
			"SkipWhitespace":    6,
			"LocateNewline":     -1,
			"ValidateUTF8":      9,
		},
	},
	{
		name:  "newline_at_32",
		input: []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"),
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
		name:  "utf8_two_byte_then_invalid",
		input: append([]byte("hello é"), 0x80),
		want: map[string]int{
			"ScanBareKey":       5,
			"ScanBasicString":   9,
			"ScanLiteralString": 9,
			"SkipWhitespace":    0,
			"LocateNewline":     -1,
			"ValidateUTF8":      8,
		},
	},
}

// swapDispatch reassigns a package-level dispatch variable to a forced
// implementation for the duration of a test and restores it via
// t.Cleanup. The per-arch dispatch tests use this to pin each variant
// (SSE2/AVX2/NEON/SWAR) and then call the exported API to verify the
// dispatch path through that variant.
//
// All dispatch vars in this package share the type func([]byte) int, so
// a non-generic signature is sufficient — keeping this concrete avoids
// pulling type parameters into the test surface for no benefit.
func swapDispatch(t *testing.T, target *func([]byte) int, replacement func([]byte) int) {
	t.Helper()
	saved := *target
	*target = replacement
	t.Cleanup(func() { *target = saved })
}

// runDispatchSmoke executes dispatchSmokeCases against the ten exported
// scan kernels, comparing each returned offset against the want map for
// the case. Callers MUST have pinned the variants under test via
// swapDispatch before invoking this helper; runDispatchSmoke itself is
// implementation-agnostic — it only goes through the public API.
//
// This function intentionally does not call t.Parallel(): swapDispatch
// mutates package-level state for the calling test's scope only, and
// running parallel siblings would race on those vars.
func runDispatchSmoke(t *testing.T) {
	t.Helper()
	for _, c := range dispatchSmokeCases {
		t.Run(c.name, func(t *testing.T) {
			if want := c.want["ScanBareKey"]; ScanBareKey(c.input) != want {
				t.Errorf("ScanBareKey(%q) = %d, want %d", c.input, ScanBareKey(c.input), want)
			}
			if want := c.want["ScanBasicString"]; ScanBasicString(c.input) != want {
				t.Errorf("ScanBasicString(%q) = %d, want %d", c.input, ScanBasicString(c.input), want)
			}
			if got, want := ScanBasicStringStrict(c.input), naiveScanBasicStringStrict(c.input); got != want {
				t.Errorf("ScanBasicStringStrict(%q) = %d, want %d", c.input, got, want)
			}
			if got, want := ScanCommentBody(c.input), naiveScanCommentBody(c.input); got != want {
				t.Errorf("ScanCommentBody(%q) = %d, want %d", c.input, got, want)
			}
			if got, want := ScanBareValueEnd(c.input), naiveScanBareValueEnd(c.input); got != want {
				t.Errorf("ScanBareValueEnd(%q) = %d, want %d", c.input, got, want)
			}
			if got, want := CountLines(c.input), naiveCountLines(c.input); got != want {
				t.Errorf("CountLines(%q) = %d, want %d", c.input, got, want)
			}
			if want := c.want["ScanLiteralString"]; ScanLiteralString(c.input) != want {
				t.Errorf("ScanLiteralString(%q) = %d, want %d", c.input, ScanLiteralString(c.input), want)
			}
			if want := c.want["SkipWhitespace"]; SkipWhitespace(c.input) != want {
				t.Errorf("SkipWhitespace(%q) = %d, want %d", c.input, SkipWhitespace(c.input), want)
			}
			if want := c.want["LocateNewline"]; LocateNewline(c.input) != want {
				t.Errorf("LocateNewline(%q) = %d, want %d", c.input, LocateNewline(c.input), want)
			}
			if want := c.want["ValidateUTF8"]; ValidateUTF8(c.input) != want {
				t.Errorf("ValidateUTF8(%q) = %d, want %d", c.input, ValidateUTF8(c.input), want)
			}
		})
	}
}

// pinAllDispatch is a small convenience: call swapDispatch on each of
// the ten dispatch vars in a single call so a forcing test reads
// linearly instead of as separate swaps.
func pinAllDispatch(t *testing.T,
	bareKey, basicString, basicStringStrict, commentBody, bareValueEnd, countLines, literalString, ws, newline, utf8 func([]byte) int,
) {
	t.Helper()
	swapDispatch(t, &scanBareIdent, bareKey)
	swapDispatch(t, &scanBasicString, basicString)
	swapDispatch(t, &scanBasicStringStrict, basicStringStrict)
	swapDispatch(t, &scanCommentBody, commentBody)
	swapDispatch(t, &scanBareValueEnd, bareValueEnd)
	swapDispatch(t, &countLines, countLines)
	swapDispatch(t, &scanLiteralString, literalString)
	swapDispatch(t, &skipWhitespace, ws)
	swapDispatch(t, &locateNewline, newline)
	swapDispatch(t, &validateUTF8, utf8)
}
