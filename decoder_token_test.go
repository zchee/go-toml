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
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

func TestDecoderTokenStream_BasicKeyValue(t *testing.T) {
	dec := NewDecoderBytes([]byte("name = \"Alice\"\nactive = true\n[server]\nports = [80, 443]\n"))

	expects := []TokenKind{
		TokenKindKey,
		TokenKindValueString,
		TokenKindKey,
		TokenKindValueBool,
		TokenKindTableHeader,
		TokenKindKey,
		TokenKindArrayStart,
		TokenKindValueInteger,
		TokenKindValueInteger,
		TokenKindArrayEnd,
	}
	for i, wantKind := range expects {
		tok, err := dec.ReadToken()
		if err != nil {
			t.Fatalf("ReadToken #%d error = %v", i, err)
		}
		if got := tok.Kind; got != wantKind {
			t.Fatalf("ReadToken #%d kind=%q, want=%q", i, got, wantKind)
		}
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadToken after stream = %v, want EOF", err)
	}
}

func TestDecoderTokenOffsets(t *testing.T) {
	t.Parallel()

	input := []byte("name = \"Alice\"\nactive = true\n")
	dec := NewDecoderBytes(input)
	tokens := mustReadAllTokens(t, dec)
	wants := []struct {
		text   string
		offset int
	}{
		{text: "name", offset: 0},
		{text: "\"Alice\"", offset: len("name = ")},
		{text: "active", offset: len("name = \"Alice\"\n")},
		{text: "true", offset: len("name = \"Alice\"\nactive = ")},
	}
	if len(tokens) != len(wants) {
		t.Fatalf("ReadToken count = %d, want %d: %#v", len(tokens), len(wants), tokens)
	}
	for i, want := range wants {
		if string(tokens[i].Bytes) != want.text || tokens[i].Offset != want.offset {
			t.Fatalf("token[%d] = (%q, offset %d), want (%q, offset %d)", i, tokens[i].Bytes, tokens[i].Offset, want.text, want.offset)
		}
	}
}

func TestDecoderTokenLineColCompatibilityFields(t *testing.T) {
	t.Parallel()

	input := []byte("# top\r\nnext = true\n")
	tokens := mustReadAllTokens(t, NewDecoderBytes(input))
	wants := []struct {
		name   string
		index  int
		offset int
		line   int
		col    int
	}{
		{name: "comment", index: 0, offset: 0, line: 1, col: 1},
		{name: "key", index: 1, offset: len("# top\r\n"), line: 2, col: 1},
		{name: "value", index: 2, offset: len("# top\r\nnext = "), line: 2, col: 8},
	}
	if len(tokens) != len(wants) {
		t.Fatalf("ReadToken count = %d, want %d: %#v", len(tokens), len(wants), tokens)
	}
	for _, want := range wants {
		got := tokens[want.index]
		if got.Offset != want.offset || got.Line != want.line || got.Col != want.col {
			t.Fatalf("%s token position = offset %d line %d col %d, want offset %d line %d col %d", want.name, got.Offset, got.Line, got.Col, want.offset, want.line, want.col)
		}
	}

	_ = Token{Offset: 0, Line: 1, Col: 1}
}

func TestDecoderSyntaxErrorLineColumnAfterLazyAdvance(t *testing.T) {
	t.Parallel()

	_, err := readAllTokens(NewDecoderBytes([]byte("first = 1\nsecond = ")))
	if err == nil {
		t.Fatal("ReadToken() error = nil, want expected-value syntax error")
	}
	const want = "toml: expected value at line 2 col 10"
	if got := err.Error(); got != want {
		t.Fatalf("ReadToken() error = %q, want %q", got, want)
	}
}

func TestDecoderCommentKernelPreservesCRLFAndControls(t *testing.T) {
	t.Parallel()

	t.Run("success:comment stops before crlf", func(t *testing.T) {
		t.Parallel()
		input := []byte("# top comment\r\nnext = 1\n")
		tokens := mustReadAllTokens(t, NewDecoderBytes(input))
		if len(tokens) < 3 {
			t.Fatalf("token count = %d, want at least comment/key/value: %#v", len(tokens), tokens)
		}
		if got, want := publicToken(tokens[0]), (tokenPublic{Kind: TokenKindComment, Bytes: []byte("# top comment"), Offset: 0}); !slices.Equal(got.Bytes, want.Bytes) || got.Kind != want.Kind || got.Offset != want.Offset {
			t.Fatalf("comment token = %#v, want %#v", got, want)
		}
		if got, want := publicToken(tokens[1]), (tokenPublic{Kind: TokenKindKey, Bytes: []byte("next"), Offset: len("# top comment\r\n")}); !slices.Equal(got.Bytes, want.Bytes) || got.Kind != want.Kind || got.Offset != want.Offset {
			t.Fatalf("post-comment key = %#v, want %#v", got, want)
		}
	})

	tests := map[string]string{
		"error:lone_cr": "# bad\rnotlf",
		"error:nul":     "# bad\x00\n",
		"error:del":     "# bad\x7f\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := readAllTokens(NewDecoderBytes([]byte(input)))
			if err == nil {
				t.Fatal("ReadToken() error = nil, want comment control syntax error")
			}
			const want = "toml: control character in comment at line 1 col 6"
			if got := err.Error(); got != want {
				t.Fatalf("ReadToken() error = %q, want %q", got, want)
			}
		})
	}
}

func TestDecoderBareValueKernelKeepsParserDatetimeSemantics(t *testing.T) {
	t.Parallel()

	t.Run("success:local datetime may contain one grammar space", func(t *testing.T) {
		t.Parallel()
		input := []byte("when = 1979-05-27 07:32:00\nnext = true\n")
		tokens := mustReadAllTokens(t, NewDecoderBytes(input))
		if len(tokens) < 4 {
			t.Fatalf("token count = %d, want key/value/key/value: %#v", len(tokens), tokens)
		}
		got := publicToken(tokens[1])
		want := tokenPublic{Kind: TokenKindValueDatetime, Bytes: []byte("1979-05-27 07:32:00"), Offset: len("when = ")}
		if !slices.Equal(got.Bytes, want.Bytes) || got.Kind != want.Kind || got.Offset != want.Offset {
			t.Fatalf("datetime value = %#v, want %#v", got, want)
		}
	})

	t.Run("success:hash delimiter starts comment without required space", func(t *testing.T) {
		t.Parallel()
		input := []byte("ok = true#inline\n")
		tokens := mustReadAllTokens(t, NewDecoderBytes(input))
		wants := []tokenPublic{
			{Kind: TokenKindKey, Bytes: []byte("ok"), Offset: 0},
			{Kind: TokenKindValueBool, Bytes: []byte("true"), Offset: len("ok = ")},
			{Kind: TokenKindComment, Bytes: []byte("#inline"), Offset: len("ok = true")},
		}
		if len(tokens) != len(wants) {
			t.Fatalf("token count = %d, want %d: %#v", len(tokens), len(wants), tokens)
		}
		for i, want := range wants {
			got := publicToken(tokens[i])
			if !slices.Equal(got.Bytes, want.Bytes) || got.Kind != want.Kind || got.Offset != want.Offset {
				t.Fatalf("token[%d] = %#v, want %#v", i, got, want)
			}
		}
	})
}

func TestLineColForOffsetUsesLFCountOnlyWithCRDisplayRule(t *testing.T) {
	t.Parallel()

	data := []byte("a\r\nb\rc\n")
	tests := map[string]struct {
		off      int
		wantLine int
		wantCol  int
	}{
		"success:start":          {off: 0, wantLine: 1, wantCol: 1},
		"success:on_lf":          {off: len("a\r"), wantLine: 1, wantCol: 2},
		"success:after_crlf":     {off: len("a\r\n"), wantLine: 2, wantCol: 1},
		"success:after_lone_cr":  {off: len("a\r\nb\r"), wantLine: 2, wantCol: 2},
		"success:after_final_lf": {off: len(data), wantLine: 3, wantCol: 1},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gotLine, gotCol := lineColForOffset(data, tc.off)
			if gotLine != tc.wantLine || gotCol != tc.wantCol {
				t.Fatalf("lineColForOffset(%q, %d) = (%d,%d), want (%d,%d)", data, tc.off, gotLine, gotCol, tc.wantLine, tc.wantCol)
			}
		})
	}
}

func TestParseDocumentSemanticErrorLineColumnFromTokenOffset(t *testing.T) {
	t.Parallel()

	_, err := parseDocument([]byte("a = 1\nb = 2\na = 3\n"), nil, nil)
	if err == nil {
		t.Fatal("parseDocument() error = nil, want duplicate-key syntax error")
	}
	const want = "toml: duplicate key at line 3 col 1"
	if got := err.Error(); got != want {
		t.Fatalf("parseDocument() error = %q, want %q", got, want)
	}
}

func TestHasBytePrefixForTripleQuotedStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    string
		quote byte
		want  bool
	}{
		{name: "basic string", in: "\"\"\"multi", quote: '"', want: true},
		{name: "literal string", in: "'''multi", quote: '\'', want: true},
		{name: "wrong quote", in: "\"\"\"multi", quote: '\'', want: false},
		{name: "too short", in: "''", quote: '\'', want: false},
		{name: "single quoted string", in: "'value'", quote: '\'', want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasBytePrefix([]byte(tc.in), tc.quote, tc.quote, tc.quote); got != tc.want {
				t.Fatalf("hasBytePrefix(%q, %q, %q, %q) = %v, want %v", tc.in, tc.quote, tc.quote, tc.quote, got, tc.want)
			}
		})
	}
}

func TestDecoderTokenStream_CommentAndHeaderAtLineStart(t *testing.T) {
	dec := NewDecoderBytes([]byte("# file\n[app]\n# row\nvalue = 1\n"))

	expects := []TokenKind{
		TokenKindComment,
		TokenKindTableHeader,
		TokenKindComment,
		TokenKindKey,
		TokenKindValueInteger,
	}
	for i, wantKind := range expects {
		tok, err := dec.ReadToken()
		if err != nil {
			t.Fatalf("ReadToken #%d error = %v", i, err)
		}
		if tok.Kind != wantKind {
			t.Fatalf("ReadToken #%d kind=%q, want=%q", i, tok.Kind, wantKind)
		}
	}
}

func TestDecoderTokenGolden_FixtureCorpus(t *testing.T) {
	base := "testdata/tokens"
	entries := mustListTokenFixtures(t, base)
	if len(entries) == 0 {
		t.Fatal("expected at least one token fixture in testdata/tokens")
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".toml")
		t.Run(entry.Name(), func(t *testing.T) {
			input := mustReadRepoFile(t, filepath.Join(base, entry.Name()))
			got := mustReadAllTokens(t, NewDecoderBytes(input))
			wantPath := filepath.Join(base, name+".tokens.golden")
			want := readTokenGolden(t, mustRepoPath(t, wantPath))
			if len(got) != len(want) {
				t.Fatalf("token count mismatch: got=%d want=%d", len(got), len(want))
			}
			for i := range got {
				if string(got[i].Bytes) != want[i].Text {
					t.Fatalf("token[%d] bytes mismatch: got=%q want=%q", i, got[i].Bytes, want[i].Text)
				}
				if got[i].Kind != tokenKindFromString(want[i].Kind) {
					t.Fatalf("token[%d] kind mismatch: got=%q want=%q", i, got[i].Kind, want[i].Kind)
				}
			}
		})
	}
}

func TestDecoderTokenGolden_InvalidUTF8Fixture(t *testing.T) {
	input := mustReadRepoFile(t, "testdata/tokens/invalid-utf8.toml")
	dec := NewDecoderBytes(input)
	if _, err := readAllTokens(dec); err == nil {
		t.Fatalf("expected invalid-utf8 fixture to fail")
	}
}

func TestDecoderTokenGolden_ReaderAndBytesParityForFixtureCorpus(t *testing.T) {
	base := "testdata/tokens"
	entries := mustListTokenFixtures(t, base)
	if len(entries) == 0 {
		t.Fatal("expected at least one token fixture in testdata/tokens")
	}
	for _, entry := range entries {
		path := filepath.Join(base, entry.Name())
		t.Run(entry.Name(), func(t *testing.T) {
			input := mustReadRepoFile(t, path)
			gotKinds, err := readTokenKinds(NewDecoderBytes(input))
			if err != nil {
				t.Fatalf("bytes constructor failed: %v", err)
			}
			gotReaderKinds, err := readTokenKinds(NewDecoder(strings.NewReader(string(input))))
			if err != nil {
				t.Fatalf("reader constructor failed: %v", err)
			}
			if len(gotKinds) != len(gotReaderKinds) {
				t.Fatalf("token kind count mismatch: bytes=%d reader=%d", len(gotKinds), len(gotReaderKinds))
			}
			for i := range gotKinds {
				if gotKinds[i].Kind != gotReaderKinds[i].Kind {
					t.Fatalf("token[%d] mismatch: bytes=%q reader=%q", i, gotKinds[i].Kind, gotReaderKinds[i].Kind)
				}
			}
		})
	}
}

func TestDecoderValueKindAndValidation(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name string
		in   string
		kind TokenKind
	}{
		{name: "boolean", in: "a = true", kind: TokenKindValueBool},
		{name: "integer", in: "a = +12_34", kind: TokenKindValueInteger},
		{name: "float", in: "a = 12.34e+1", kind: TokenKindValueFloat},
		{name: "special float inf", in: "a = inf", kind: TokenKindValueFloat},
		{name: "special float negative inf", in: "a = -inf", kind: TokenKindValueFloat},
		{name: "special float nan", in: "a = nan", kind: TokenKindValueFloat},
		{name: "special float negative nan", in: "a = -nan", kind: TokenKindValueFloat},
		{name: "datetime-z", in: "a = 1987-07-05T17:45:00Z", kind: TokenKindValueDatetime},
		{name: "datetime-z without seconds", in: "a = 1987-07-05T17:45Z", kind: TokenKindValueDatetime},
		{name: "date", in: "a = 1979-05-27", kind: TokenKindValueDatetime},
		{name: "time", in: "a = 07:32:00", kind: TokenKindValueDatetime},
		{name: "time without seconds", in: "a = 07:32", kind: TokenKindValueDatetime},
	}

	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dec := NewDecoderBytes([]byte(tc.in))
			if _, err := dec.ReadToken(); err != nil {
				t.Fatalf("ReadToken key = %v", err)
			}
			tok, err := dec.ReadToken()
			if err != nil {
				t.Fatalf("ReadToken value = %v", err)
			}
			if tok.Kind != tc.kind {
				t.Fatalf("ReadToken kind = %q, want %q", tok.Kind, tc.kind)
			}
		})
	}

	invalid := []string{
		"a = True",
		"a = False",
		"a = INF",
		"a = NaN",
		"a = 2025-1-1",             // invalid date format
		"a = 2025-01-01T24:00:00Z", // invalid hour
		"a = 1e+",
		"a = 1_",
		"a = 18446744073709551616",
	}
	for _, in := range invalid {
		t.Run("invalid: "+in, func(t *testing.T) {
			t.Parallel()
			dec := NewDecoderBytes([]byte(in))
			for i := range 2 {
				if _, err := dec.ReadToken(); err != nil {
					// key token should not fail for these cases.
					if i == 0 {
						t.Fatalf("first token key parse = %v", err)
					}
					break
				}
				if i == 1 {
					t.Fatalf("expected syntax error for %q", in)
				}
			}
		})
	}
}

func TestUnmarshalSpecialFloatValues(t *testing.T) {
	t.Parallel()

	var dst map[string]any
	if err := Unmarshal([]byte("nan = nan\nnan_neg = -nan\nnan_plus = +nan\ninfinity = inf\ninfinity_neg = -inf\ninfinity_plus = +inf\n"), &dst); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for _, key := range []string{"nan", "nan_neg", "nan_plus"} {
		got, ok := dst[key].(float64)
		if !ok {
			t.Fatalf("%s = %T(%#v), want float64 NaN", key, dst[key], dst[key])
		}
		if !math.IsNaN(got) {
			t.Fatalf("%s = %v, want NaN", key, got)
		}
	}
	for _, tc := range []struct {
		key  string
		want float64
	}{
		{key: "infinity", want: math.Inf(1)},
		{key: "infinity_plus", want: math.Inf(1)},
		{key: "infinity_neg", want: math.Inf(-1)},
	} {
		got, ok := dst[tc.key].(float64)
		if !ok {
			t.Fatalf("%s = %T(%#v), want float64", tc.key, dst[tc.key], dst[tc.key])
		}
		if got != tc.want {
			t.Fatalf("%s = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestDecoderDateTimeValueFormsRoundTrip(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value string
	}{
		"success: offset datetime utc": {
			value: "1979-05-27T07:32:00Z",
		},
		"success: offset datetime plus fourteen boundary": {
			value: "1979-05-27T07:32:00+14:00",
		},
		"success: offset datetime minus twelve boundary": {
			value: "1979-05-27T07:32:00-12:00",
		},
		"success: local datetime": {
			value: "1979-05-27T07:32:00",
		},
		"success: local datetime space separator": {
			value: "1979-05-27 07:32:00",
		},
		"success: local date": {
			value: "1979-05-27",
		},
		"success: local time": {
			value: "07:32:00",
		},
		"success: fractional precision zero digits absent": {
			value: "1979-05-27T07:32:00Z",
		},
		"success: fractional precision one digit": {
			value: "1979-05-27T07:32:00.1Z",
		},
		"success: fractional precision two digits": {
			value: "1979-05-27T07:32:00.12Z",
		},
		"success: fractional precision three digits": {
			value: "1979-05-27T07:32:00.123Z",
		},
		"success: fractional precision four digits": {
			value: "1979-05-27T07:32:00.1234Z",
		},
		"success: fractional precision five digits": {
			value: "1979-05-27T07:32:00.12345Z",
		},
		"success: fractional precision six digits": {
			value: "1979-05-27T07:32:00.123456Z",
		},
		"success: fractional precision seven digits": {
			value: "1979-05-27T07:32:00.1234567Z",
		},
		"success: fractional precision eight digits": {
			value: "1979-05-27T07:32:00.12345678Z",
		},
		"success: fractional precision nine digits": {
			value: "1979-05-27T07:32:00.123456789Z",
		},
		"success: local time fractional precision nine digits": {
			value: "07:32:00.123456789",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := readSingleValueToken(t, "when = "+tc.value)
			want := Token{Kind: TokenKindValueDatetime, Bytes: []byte(tc.value), Offset: len("when = ")}
			if diff := gocmp.Diff(publicToken(want), publicToken(got)); diff != "" {
				t.Fatalf("datetime value token mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

type tokenPublic struct {
	Kind   TokenKind
	Bytes  []byte
	Offset int
}

func publicToken(tok Token) tokenPublic {
	return tokenPublic{Kind: tok.Kind, Bytes: tok.Bytes, Offset: tok.Offset}
}

func TestLooksLikeDateTimeFastRejectsNonDateTimeValues(t *testing.T) {
	tests := []string{
		"true",
		"false",
		"42",
		"3.14159",
		"1.0.0",
		"nan",
		"pandaemonium",
	}

	for _, value := range tests {
		t.Run(value, func(t *testing.T) {
			raw := []byte(value)
			var got bool
			allocs := testing.AllocsPerRun(1000, func() {
				got = looksLikeDatetime(raw)
			})
			if got {
				t.Fatalf("looksLikeDatetime(%q) = true, want false", value)
			}
			if allocs != 0 {
				t.Fatalf("looksLikeDatetime(%q) allocations = %v, want 0", value, allocs)
			}
		})
	}
}

func TestDecoderBareScalarClassificationAvoidsNormalizationAllocation(t *testing.T) {
	tests := map[string]struct {
		input string
		kind  TokenKind
	}{
		"success: integer underscores":          {input: "v = 1_2_3\n", kind: TokenKindValueInteger},
		"success: prefixed integer underscores": {input: "v = 0xdead_beef\n", kind: TokenKindValueInteger},
		"success: float underscores exponent":   {input: "v = 1_2.3E+4\n", kind: TokenKindValueFloat},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			input := []byte(tc.input)
			var got Token
			valid := true
			allocs := testing.AllocsPerRun(1000, func() {
				dec := newAllocationHotPathDecoder(input)
				if _, err := dec.ReadToken(); err != nil {
					valid = false
					return
				}
				tok, err := dec.ReadToken()
				if err != nil {
					valid = false
					return
				}
				got = tok
			})
			if !valid {
				t.Fatalf("ReadToken(%q) failed", tc.input)
			}
			if got.Kind != tc.kind {
				t.Fatalf("ReadToken(%q) kind=%s, want %s", tc.input, got.Kind, tc.kind)
			}
			wantAllocs := 0.0
			if tc.kind == TokenKindValueFloat {
				wantAllocs = 1
			}
			if allocs > wantAllocs {
				t.Fatalf("ReadToken(%q) allocs/run = %v, want <= %v", tc.input, allocs, wantAllocs)
			}
		})
	}
}

func newAllocationHotPathDecoder(input []byte) Decoder {
	dec := Decoder{
		atLineStart: true,
		buf:         input,
		limits: Limits{
			MaxNestedDepth:  DefaultMaxNestedDepth,
			MaxKeyLength:    DefaultMaxKeyLength,
			MaxStringLength: DefaultMaxStringLength,
			MaxDocumentSize: DefaultMaxDocumentSize,
		},
	}
	dec.skipBOM()
	dec.decodeStart = dec.off
	return dec
}

func TestDecoderDateTimeInvalidForms(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value string
	}{
		"error: local date uses one digit month": {
			value: "1979-5-27",
		},
		"error: local date uses one digit day": {
			value: "1979-05-7",
		},
		"error: local date has impossible day": {
			value: "1979-02-29",
		},
		"error: local time has invalid hour": {
			value: "24:00:00",
		},
		"error: local time has invalid minute": {
			value: "23:60:00",
		},
		"error: local time has invalid second": {
			value: "23:59:60",
		},
		"error: offset datetime has trailing fractional decimal": {
			value: "1979-05-27T07:32:00.Z",
		},
		"error: offset datetime omits offset minute": {
			value: "1979-05-27T07:32:00+14",
		},
		"error: offset datetime omits offset colon": {
			value: "1979-05-27T07:32:00+1400",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dec := NewDecoderBytes([]byte("when = " + tc.value))
			if tok, err := dec.ReadToken(); err != nil || tok.Kind != TokenKindKey {
				t.Fatalf("ReadToken key = %v, %v; want key before datetime validation", tok, err)
			}
			if tok, err := dec.ReadToken(); err == nil {
				t.Fatalf("ReadToken value = %v, nil; want syntax error for %q", tok, tc.value)
			}
		})
	}
}

func TestDecoderErrorStateIsSticky(t *testing.T) {
	dec := NewDecoderBytes([]byte("broken = [\n"))
	tok, err := dec.ReadToken()
	if err != nil || tok.Kind != TokenKindKey {
		t.Fatalf("ReadToken key = %v, %v", tok, err)
	}
	tok, err = dec.ReadToken()
	if err != nil || tok.Kind != TokenKindArrayStart {
		t.Fatalf("ReadToken array start = %v, %v", tok, err)
	}
	_, err1 := dec.ReadToken()
	if err1 == nil {
		t.Fatalf("expected parse error from malformed array open")
	}
	_, err2 := dec.ReadToken()
	if err2 == nil {
		t.Fatalf("expected sticky error on repeated ReadToken calls")
	}
	if !errors.Is(err1, err2) {
		t.Fatalf("sticky error mismatch: first=%v second=%v", err1, err2)
	}
}

func TestDecoderReaderAndBytesConstructorsMatch(t *testing.T) {
	input := []byte("title = \"Toml\"\na = 1\n")
	wantKinds, err := readAllTokens(NewDecoderBytes(input))
	if err != nil {
		t.Fatalf("ReadToken with NewDecoderBytes = %v", err)
	}

	gotKinds, err := readAllTokens(NewDecoder(strings.NewReader(string(input))))
	if err != nil {
		t.Fatalf("ReadToken with NewDecoder = %v", err)
	}
	if len(wantKinds) != len(gotKinds) {
		t.Fatalf("token count mismatch: bytes=%d reader=%d", len(wantKinds), len(gotKinds))
	}
	for i := range wantKinds {
		if wantKinds[i].Kind != gotKinds[i].Kind {
			t.Fatalf("token[%d] mismatch: bytes=%q reader=%q", i, wantKinds[i].Kind, gotKinds[i].Kind)
		}
	}
}

func TestDecoderEOF(t *testing.T) {
	dec := NewDecoderBytes([]byte{})
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF on empty input, got %v", err)
	}
}

func TestDecoderKeyLimitError(t *testing.T) {
	tooLong := strings.Repeat("a", MaxKeyLength+1)
	dec := NewDecoderBytes([]byte(tooLong+" = 1\n"), WithLimits(Limits{MaxKeyLength: MaxKeyLength}))
	_, err := dec.ReadToken()
	if err == nil {
		t.Fatalf("ReadToken expected limit error")
	}
	if !strings.Contains(err.Error(), "MaxKeyLength") {
		t.Fatalf("error=%v, want MaxKeyLength mention", err)
	}
}

func TestDecoderDocumentSizeLimit(t *testing.T) {
	dec := NewDecoderBytes([]byte("name = \"Alice\""), WithLimits(Limits{MaxDocumentSize: 4}))
	_, err := dec.ReadToken()
	if err == nil {
		t.Fatalf("ReadToken expected document size limit error")
	}
	if !strings.Contains(err.Error(), "MaxDocumentSize") {
		t.Fatalf("error=%v, want MaxDocumentSize mention", err)
	}
}

func mustListTokenFixtures(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(mustRepoPath(t, dir))
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v", dir, err)
	}
	var fixtures []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tokens.golden") {
			continue
		}
		if e.Name() == "invalid-utf8.toml" {
			continue
		}
		fixtures = append(fixtures, e)
	}
	slices.SortFunc(fixtures, func(x, y os.DirEntry) int {
		return cmp.Compare(x.Name(), y.Name())
	})
	return fixtures
}

func readTokenKinds(dec *Decoder) ([]Token, error) {
	return readAllTokens(dec)
}

func mustReadAllTokens(t testing.TB, dec *Decoder) []Token {
	t.Helper()
	tokens, err := readAllTokens(dec)
	if err != nil {
		t.Fatalf("read token stream = %v", err)
	}
	return tokens
}

type tokenGolden struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func readTokenGolden(t testing.TB, path string) []tokenGolden {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open(%s) error = %v", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	got := make([]tokenGolden, 0)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var item tokenGolden
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("invalid golden format %s:%d: %v", path, lineNo, err)
		}
		got = append(got, item)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan golden file %s error = %v", path, err)
	}
	return got
}

func tokenKindFromString(name string) TokenKind {
	switch name {
	case "Invalid":
		return TokenKindInvalid
	case "TableHeader":
		return TokenKindTableHeader
	case "ArrayTableHeader":
		return TokenKindArrayTableHeader
	case "Key":
		return TokenKindKey
	case "ValueString":
		return TokenKindValueString
	case "ValueInteger":
		return TokenKindValueInteger
	case "ValueFloat":
		return TokenKindValueFloat
	case "ValueBool":
		return TokenKindValueBool
	case "ValueDatetime":
		return TokenKindValueDatetime
	case "ArrayStart":
		return TokenKindArrayStart
	case "ArrayEnd":
		return TokenKindArrayEnd
	case "InlineTableStart":
		return TokenKindInlineTableStart
	case "InlineTableEnd":
		return TokenKindInlineTableEnd
	case "Comment":
		return TokenKindComment
	default:
		panic(fmt.Sprintf("unsupported token kind in fixture: %q", name))
	}
}

func readAllTokens(dec *Decoder) ([]Token, error) {
	tokens := make([]Token, 0)
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return tokens, nil
			}
			return nil, err
		}
		tokens = append(tokens, tok)
	}
}

func readSingleValueToken(t testing.TB, input string) Token {
	t.Helper()
	dec := NewDecoderBytes([]byte(input))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind != TokenKindKey {
		t.Fatalf("ReadToken key = %v, %v; want key before value", tok, err)
	}
	tok, err := dec.ReadToken()
	if err != nil {
		t.Fatalf("ReadToken value = %v", err)
	}
	return tok
}

func mustReadRepoFile(t testing.TB, rel string) []byte {
	t.Helper()
	path := mustRepoPath(t, rel)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}
	return body
}

func mustRepoPath(t testing.TB, rel string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	repoRoot := filepath.Dir(file)
	return filepath.Join(repoRoot, filepath.FromSlash(rel))
}

func TestHasBytePrefix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  []byte
		want []byte
		ok   bool
	}{
		{name: "triple double", raw: []byte("\"\"\"value"), want: []byte{'"', '"', '"'}, ok: true},
		{name: "triple literal", raw: []byte("'''value"), want: []byte{'\'', '\'', '\''}, ok: true},
		{name: "too short", raw: []byte("\"\""), want: []byte{'"', '"', '"'}, ok: false},
		{name: "mismatch", raw: []byte("\"'\""), want: []byte{'"', '"', '"'}, ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasBytePrefix(tc.raw, tc.want...); got != tc.ok {
				t.Fatalf("hasBytePrefix(%q, %q...) = %v, want %v", tc.raw, tc.want, got, tc.ok)
			}
		})
	}
}

// TestDecoder_IndentedTableHeaderAtLineStart guards the skipSpaces
// regression: leading whitespace before a [header] or [[header]] at line
// start must still be classified as a table-header token, not as a value.
func TestDecoder_IndentedTableHeaderAtLineStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []TokenKind
	}{
		{
			name: "indented subtable under array of tables",
			in:   "[[cascade]]\n  [cascade._target]\n    kind = \"page\"\n",
			want: []TokenKind{
				TokenKindArrayTableHeader,
				TokenKindTableHeader,
				TokenKindKey,
				TokenKindValueString,
			},
		},
		{
			name: "indented subtable under table",
			in:   "[a]\n  [a.b]\n    kind = \"page\"\n",
			want: []TokenKind{
				TokenKindTableHeader,
				TokenKindTableHeader,
				TokenKindKey,
				TokenKindValueString,
			},
		},
		{
			name: "tab-indented array-of-tables header",
			in:   "\t[[cascade]]\n",
			want: []TokenKind{TokenKindArrayTableHeader},
		},
		{
			name: "space-indented table header at file start",
			in:   "    [server]\n",
			want: []TokenKind{TokenKindTableHeader},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			toks, err := readTokenKinds(NewDecoderBytes([]byte(tc.in)))
			if err != nil {
				t.Fatalf("readTokenKinds(%q) error = %v", tc.in, err)
			}
			got := make([]TokenKind, len(toks))
			for i, tok := range toks {
				got[i] = tok.Kind
			}
			if diff := gocmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("token kinds mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestUnmarshal_InlineTableMultipleEntries guards the inline-table
// state-machine regression: a comma inside `{ ... }` introduces the next
// key, not the next value.
func TestUnmarshal_InlineTableMultipleEntries(t *testing.T) {
	t.Parallel()

	type Name struct {
		First string
		Last  string
	}
	type Point struct {
		X int64
		Y int64
	}
	type Doc struct {
		Name  Name
		Point Point
	}

	tests := []struct {
		name string
		in   string
		want Doc
	}{
		{
			name: "two-entry inline table of strings",
			in:   "name = { first = \"Tom\", last = \"Preston-Werner\" }\n",
			want: Doc{Name: Name{First: "Tom", Last: "Preston-Werner"}},
		},
		{
			name: "two-entry inline table of integers",
			in:   "point = { x = 1, y = 2 }\n",
			want: Doc{Point: Point{X: 1, Y: 2}},
		},
		{
			name: "both inline tables in same document",
			in:   "name = { first = \"Tom\", last = \"Preston-Werner\" }\npoint = { x = 1, y = 2 }\n",
			want: Doc{
				Name:  Name{First: "Tom", Last: "Preston-Werner"},
				Point: Point{X: 1, Y: 2},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got Doc
			if err := Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("Unmarshal(%q) error = %v", tc.in, err)
			}
			if diff := gocmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("Unmarshal result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestDecoder_InlineTableTokenStream verifies the token-stream contract
// after a comma inside an inline table: the next non-whitespace token must
// be a key, not a value.
func TestDecoder_InlineTableTokenStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []TokenKind
	}{
		{
			name: "two-entry inline table",
			in:   "name = { first = \"Tom\", last = \"PW\" }\n",
			want: []TokenKind{
				TokenKindKey,
				TokenKindInlineTableStart,
				TokenKindKey,
				TokenKindValueString,
				TokenKindKey,
				TokenKindValueString,
				TokenKindInlineTableEnd,
			},
		},
		{
			name: "nested inline table",
			in:   "v = { a = 1, b = { x = 1, y = 2 } }\n",
			want: []TokenKind{
				TokenKindKey,
				TokenKindInlineTableStart,
				TokenKindKey,
				TokenKindValueInteger,
				TokenKindKey,
				TokenKindInlineTableStart,
				TokenKindKey,
				TokenKindValueInteger,
				TokenKindKey,
				TokenKindValueInteger,
				TokenKindInlineTableEnd,
				TokenKindInlineTableEnd,
			},
		},
		{
			name: "inline tables inside an array",
			in:   "arr = [{a = 1, b = 2}, {a = 3, b = 4}]\n",
			want: []TokenKind{
				TokenKindKey,
				TokenKindArrayStart,
				TokenKindInlineTableStart,
				TokenKindKey,
				TokenKindValueInteger,
				TokenKindKey,
				TokenKindValueInteger,
				TokenKindInlineTableEnd,
				TokenKindInlineTableStart,
				TokenKindKey,
				TokenKindValueInteger,
				TokenKindKey,
				TokenKindValueInteger,
				TokenKindInlineTableEnd,
				TokenKindArrayEnd,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			toks, err := readTokenKinds(NewDecoderBytes([]byte(tc.in)))
			if err != nil {
				t.Fatalf("readTokenKinds(%q) error = %v", tc.in, err)
			}
			got := make([]TokenKind, len(toks))
			for i, tok := range toks {
				got[i] = tok.Kind
			}
			if diff := gocmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("token kinds mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDecoderQuotedEmptyKeysAndStringControls(t *testing.T) {
	t.Parallel()

	t.Run("valid quoted empty keys", func(t *testing.T) {
		t.Parallel()
		var got map[string]any
		if err := Unmarshal([]byte("\"\" = \"blank\"\n[a.\"\"]\nvalue = \"nested\"\n"), &got); err != nil {
			t.Fatalf("Unmarshal() quoted empty keys error = %v", err)
		}
		if got[""] != "blank" {
			t.Fatalf("top-level empty key = %v, want blank", got[""])
		}
		a, ok := got["a"].(map[string]any)
		if !ok {
			t.Fatalf("a = %T(%#v), want table", got["a"], got["a"])
		}
		empty, ok := a[""].(map[string]any)
		if !ok {
			t.Fatalf("a.empty = %T(%#v), want table", a[""], a[""])
		}
		if empty["value"] != "nested" {
			t.Fatalf("a.empty.value = %v, want nested", empty["value"])
		}
	})

	tests := []struct {
		name string
		in   string
	}{
		{name: "basic string null", in: "v = \"\x00\"\n"},
		{name: "literal string null", in: "v = '\x00'\n"},
		{name: "invalid unicode scalar", in: "v = \"\\U00110000\"\n"},
		{name: "bare empty dotted segment still invalid", in: "a. = 1\n"},
	}
	for _, tc := range tests {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := readTokenKinds(NewDecoderBytes([]byte(tc.in))); err == nil {
				t.Fatalf("readTokenKinds(%q) error = nil, want invalid TOML error", tc.in)
			}
		})
	}

	t.Run("valid TOML 1.1 x escapes", func(t *testing.T) {
		t.Parallel()
		if _, err := readTokenKinds(NewDecoderBytes([]byte("v = \"\\x41\"\n\"\\x42\" = 1\n"))); err != nil {
			t.Fatalf("readTokenKinds() TOML 1.1 x escapes error = %v", err)
		}
	})
}

func TestUnmarshal_TableAndKeyRedefinitionValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{name: "duplicate top-level key", in: "name = \"Tom\"\nname = \"Pradyun\"\n"},
		{name: "duplicate table header", in: "[a]\nb = 1\n\n[a]\nc = 2\n"},
		{name: "scalar redefined as dotted table", in: "a = false\na.b = true\n"},
		{name: "dotted scalar redefined as table", in: "[a]\nb = 1\n\n[a.b]\nc = 2\n"},
		{name: "dotted key cannot extend explicit table", in: "[a.b.c]\nz = 9\n\n[a]\nb.c.t = \"invalid\"\n"},
		{name: "inline table duplicate key", in: "a = { k = 1, k = 2 }\n"},
		{name: "inline table scalar redefined as dotted table", in: "a = { k = 1, k.name = \"joe\" }\n"},
		{name: "inline table cannot be extended by table header", in: "a = {}\n[a.b]\n"},
		{name: "inline table cannot be extended by sibling dotted key", in: "a = { inner = { dog = \"best\" }, inner.cat = \"worst\" }\n"},
		{name: "table cannot become array table", in: "[tbl]\n[[tbl]]\n"},
		{name: "array table cannot become table", in: "[[tbl]]\n[tbl]\n"},
		{name: "dotted scalar cannot become nested array table", in: "a.b = 1\n[[a.b]]\n"},
		{name: "table scalar cannot become nested array table", in: "[a]\nb = 1\n[[a.b]]\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got map[string]any
			if err := Unmarshal([]byte(tc.in), &got); err == nil {
				t.Fatalf("Unmarshal(%q) error = nil, want failure", tc.in)
			}
		})
	}
}

func TestUnmarshal_TableImplicitParentMayBecomeExplicit(t *testing.T) {
	t.Parallel()

	input := "[a.b.c]\nanswer = 42\n\n[a]\nbetter = 43\n"
	var got map[string]any
	if err := Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	a, ok := got["a"].(map[string]any)
	if !ok {
		t.Fatalf("got[a] = %T, want map[string]any", got["a"])
	}
	if got, want := a["better"], int64(43); got != want {
		t.Fatalf("a.better = %v, want %v", got, want)
	}
}

func TestUnmarshal_ArrayTableSubtableMayRepeatPerElement(t *testing.T) {
	t.Parallel()

	input := "[[arr]]\n[arr.subtab]\nval=1\n\n[[arr]]\n[arr.subtab]\nval=2\n"
	var got map[string]any
	if err := Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	arr, ok := got["arr"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("arr = %#v, want two array-table elements", got["arr"])
	}
}

func TestUnmarshal_CaseSensitiveBoolAndSpecialFloatLiterals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{name: "capital true", in: "v = True\n"},
		{name: "capital false", in: "v = False\n"},
		{name: "capital inf", in: "v = Inf\n"},
		{name: "capital nan", in: "v = NaN\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got map[string]any
			if err := Unmarshal([]byte(tc.in), &got); err == nil {
				t.Fatalf("Unmarshal(%q) error = nil, want failure", tc.in)
			}
		})
	}
}

func TestUnmarshal_TOML11FinalConformanceEdges(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name string
		in   string
	}{
		{
			name: "prefixed integer underscores",
			in:   "hex = 0xdead_beef\noct = 0o7_6_5\nbin = 0b1_0_1\n",
		},
		{
			name: "multiline basic quote run",
			in:   `str = """Here are two quotation marks: "". Simple enough."""` + "\n",
		},
		{
			name: "multiline literal starts and ends with quote runs",
			in:   "str = ''''That,' she said, 'is still pointless.''''\n",
		},
		{
			name: "raw multiline closes with five quotes",
			in:   "str = '''\nClosing with five quotes\n'''''\n",
		},
		{
			name: "escaped quote before multiline basic close",
			in:   `str = """When will it end? \"""...""\" should be here""""` + "\n",
		},
		{
			name: "array table dotted key allows later element subtable",
			in:   "[[fruits]]\nname = 'apple'\nphysical.color = 'red'\n[[fruits]]\nname = 'banana'\n[fruits.physical]\ncolor = 'yellow'\n",
		},
		{
			name: "array table inline value allows later element subtable",
			in:   "[[fruits]]\nphysical = { color = 'red' }\n[[fruits]]\n[fruits.physical]\ncolor = 'yellow'\n",
		},
	}
	for _, tc := range valid {
		t.Run("valid "+tc.name, func(t *testing.T) {
			t.Parallel()
			var got map[string]any
			if err := Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("Unmarshal(%q) error = %v", tc.in, err)
			}
		})
	}

	invalid := []struct {
		name string
		in   string
	}{
		{name: "static array table cannot be extended", in: "a = [{ b = 1 }]\n[a.c]\nfoo = 1\n"},
		{name: "empty array cannot become array table", in: "fruit = []\n[[fruit]]\n"},
		{name: "delete control in comment", in: "comment_del = \"0x7f\" # \x7f\n"},
		{name: "ideographic space is not TOML whitespace", in: "\u3000foo = \"bar\"\n"},
		{name: "newline before equals", in: "barekey\n   = 1\n"},
		{name: "newline after equals", in: "key =\n1\n"},
		{name: "value starts with equals", in: "key= = 1\n"},
		{name: "double equals", in: "a==1\n"},
		{name: "array table dotted key blocks same element subtable", in: "[[fruits]]\nphysical.color = 'red'\n[fruits.physical]\ncolor = 'green'\n"},
		{name: "array table inline value blocks same element subtable", in: "[[fruits]]\nphysical = { color = 'red' }\n[fruits.physical]\ncolor = 'green'\n"},
	}
	for _, tc := range invalid {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			t.Parallel()
			var got map[string]any
			if err := Unmarshal([]byte(tc.in), &got); err == nil {
				t.Fatalf("Unmarshal(%q) error = nil, want failure", tc.in)
			}
		})
	}
}
