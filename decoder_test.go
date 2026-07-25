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
	"bytes"
	"errors"
	"io"
	"math"
	rand "math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDecoderDecode(t *testing.T) {
	t.Parallel()

	type config struct {
		Name   string
		Server struct {
			Port int
		}
	}

	tests := map[string]struct {
		newDecoder func() *Decoder
		want       config
	}{
		"success: reader decoder decodes struct": {
			newDecoder: func() *Decoder {
				return NewDecoder(strings.NewReader("name = \"demo\"\n[server]\nport = 9443\n"))
			},
			want: config{Name: "demo", Server: struct {
				Port int
			}{Port: 9443}},
		},
		"success: byte decoder decodes struct": {
			newDecoder: func() *Decoder {
				return NewDecoderBytes([]byte("name = \"bytes\"\n[server]\nport = 8080\n"))
			},
			want: config{Name: "bytes", Server: struct {
				Port int
			}{Port: 8080}},
		},
		"success: bom-prefixed fresh decoder decodes struct": {
			newDecoder: func() *Decoder {
				return NewDecoder(strings.NewReader("\ufeffname = \"bom\"\n[server]\nport = 443\n"))
			},
			want: config{Name: "bom", Server: struct {
				Port int
			}{Port: 443}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got config
			if err := tc.newDecoder().Decode(&got); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Decode() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDecoderDecodePropagatesOptions(t *testing.T) {
	t.Parallel()

	type config struct {
		When time.Time
	}

	var got config
	dec := NewDecoder(
		strings.NewReader("when = 2026-05-20T01:02:03\n"),
		WithLocalAsUTC(),
		WithLimits(Limits{MaxDocumentSize: 64}),
	)
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.When.Location() != time.UTC {
		t.Fatalf("When.Location() = %v, want UTC", got.When.Location())
	}
	if got.When.Format(time.RFC3339) != "2026-05-20T01:02:03Z" {
		t.Fatalf("When = %s, want 2026-05-20T01:02:03Z", got.When.Format(time.RFC3339Nano))
	}

	var tooLarge config
	err := NewDecoder(
		strings.NewReader("when = 2026-05-20T01:02:03\n"),
		WithLimits(Limits{MaxDocumentSize: 4}),
	).Decode(&tooLarge)
	var limitErr *LimitError
	if !errors.As(err, &limitErr) || limitErr.Limit != "MaxDocumentSize" {
		t.Fatalf("Decode() error = %T(%v), want MaxDocumentSize LimitError", err, err)
	}
}

func TestDecoderDecodeErrors(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string
	}

	tests := map[string]struct {
		dec      *Decoder
		dst      any
		checkErr func(testing.TB, error)
	}{
		"error: nil destination matches Unmarshal": {
			dec: NewDecoder(strings.NewReader("name = \"demo\"\n")),
			dst: nil,
			checkErr: func(t testing.TB, err error) {
				t.Helper()
				var mismatch *TypeMismatchError
				if !errors.As(err, &mismatch) || mismatch.Want != "non-nil pointer" || mismatch.Got != "nil" {
					t.Fatalf("Decode() error = %T(%v), want nil destination TypeMismatchError", err, err)
				}
			},
		},
		"error: non-pointer destination matches Unmarshal": {
			dec: NewDecoder(strings.NewReader("name = \"demo\"\n")),
			dst: config{},
			checkErr: func(t testing.TB, err error) {
				t.Helper()
				var mismatch *TypeMismatchError
				if !errors.As(err, &mismatch) || mismatch.Want != "non-nil pointer" || mismatch.Got != "struct" {
					t.Fatalf("Decode() error = %T(%v), want non-pointer TypeMismatchError", err, err)
				}
			},
		},
		"error: nil decoder": {
			dec: nil,
			dst: &config{},
			checkErr: func(t testing.TB, err error) {
				t.Helper()
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("Decode() error = %T(%v), want io.ErrUnexpectedEOF", err, err)
				}
			},
		},
		"error: invalid utf-8 before destination validation": {
			dec: NewDecoderBytes([]byte{0xff}),
			dst: nil,
			checkErr: func(t testing.TB, err error) {
				t.Helper()
				var syntaxErr *SyntaxError
				if !errors.As(err, &syntaxErr) || syntaxErr.Msg != "invalid utf-8" {
					t.Fatalf("Decode() error = %T(%v), want invalid utf-8 SyntaxError", err, err)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.checkErr(t, tc.dec.Decode(tc.dst))
		})
	}
}

func TestDecoderDecodeReturnsReaderErrorFirst(t *testing.T) {
	t.Parallel()

	want := errors.New("reader failed")
	dec := NewDecoder(errorReader{err: want})
	var dst struct{}
	if err := dec.Decode(&dst); !errors.Is(err, want) {
		t.Fatalf("Decode() error = %T(%v), want reader error %v", err, err, want)
	}
}

func TestDecoderDecodeRejectsConsumedTokenStream(t *testing.T) {
	t.Parallel()

	dec := NewDecoder(strings.NewReader("name = \"demo\"\n"))
	if _, err := dec.ReadToken(); err != nil {
		t.Fatalf("ReadToken() error = %v", err)
	}

	var dst struct {
		Name string
	}
	var stateErr *DecoderStateError
	if err := dec.Decode(&dst); !errors.As(err, &stateErr) {
		t.Fatalf("Decode() error = %T(%v), want DecoderStateError", err, err)
	}
	if stateErr.Offset == 0 {
		t.Fatalf("DecoderStateError.Offset = 0, want consumed offset")
	}
}

func TestDecoderDecodeConsumesDecoder(t *testing.T) {
	t.Parallel()

	dec := NewDecoder(strings.NewReader("name = \"demo\"\n"))
	var dst struct {
		Name string
	}
	if err := dec.Decode(&dst); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if dst.Name != "demo" {
		t.Fatalf("Name = %q, want demo", dst.Name)
	}

	var second struct {
		Name string
	}
	var stateErr *DecoderStateError
	if err := dec.Decode(&second); !errors.As(err, &stateErr) {
		t.Fatalf("second Decode() error = %T(%v), want DecoderStateError", err, err)
	}
	if stateErr.Offset != len("name = \"demo\"\n") {
		t.Fatalf("DecoderStateError.Offset = %d, want %d", stateErr.Offset, len("name = \"demo\"\n"))
	}
	if second.Name != "" {
		t.Fatalf("second Decode() mutated destination to %q, want empty", second.Name)
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadToken() after Decode() error = %T(%v), want io.EOF", err, err)
	}
}

func TestDecoderDecodeCustomUnmarshalerFrom(t *testing.T) {
	t.Parallel()

	var dst decoderDecodeCustom
	if err := NewDecoder(strings.NewReader("name = \"ignored\"\n")).Decode(&dst); err != nil {
		t.Fatalf("Decode(custom) error = %v", err)
	}
	if !dst.decoded {
		t.Fatalf("Decode(custom) did not call UnmarshalTOMLFrom")
	}
}

type decoderDecodeCustom struct {
	decoded bool
}

func (c *decoderDecodeCustom) UnmarshalTOMLFrom(dec *Decoder) error {
	for {
		_, err := dec.ReadToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	c.decoded = true
	return nil
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestDecoderDeepNestedMismatchedCloser(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
	}{
		"array closer over spilled inline table": {
			input: "x = " + strings.Repeat("[", 16) + "{a=]",
		},
		"inline closer over spilled array": {
			input: "x = " + strings.Repeat("[", 15) + "{b=[}",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var m map[string]any
			if err := Unmarshal([]byte(tt.input), &m); err == nil {
				t.Fatalf("Unmarshal(%q) = nil error, want syntax error", tt.input)
			}
		})
	}
}

// TestDecoderBareCarriageReturn pins that a bare carriage return (a CR not
// immediately followed by LF) is rejected as a syntax error rather than
// hanging the tokenizer. A lone CR is invalid per the TOML spec, which only
// permits CR as part of a CRLF newline.
func TestDecoderBareCarriageReturn(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input   string
		wantMsg string
		wantCol int
	}{
		"error: lone CR after a value": {
			// The original hang seed: a value line terminated by a bare CR.
			input:   "0=0E0\r0",
			wantMsg: "bare carriage return",
			wantCol: 6,
		},
		"error: lone CR at start of input": {
			input:   "\r",
			wantMsg: "bare carriage return",
			wantCol: 1,
		},
		"error: lone CR before a key": {
			input:   "a=1\rb=2\n",
			wantMsg: "bare carriage return",
			wantCol: 4,
		},
		"error: lone CR followed by another CR": {
			input:   "a=1\r\rb=2\n",
			wantMsg: "bare carriage return",
			wantCol: 4,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			done := make(chan error, 1)
			go func() {
				var out map[string]any
				done <- Unmarshal([]byte(tt.input), &out)
			}()

			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("Unmarshal(%q) = nil error, want %q", tt.input, tt.wantMsg)
				}
				var se *SyntaxError
				if !errors.As(err, &se) {
					t.Fatalf("Unmarshal(%q) error = %T (%v), want *SyntaxError", tt.input, err, err)
				}
				if se.Msg != tt.wantMsg {
					t.Errorf("Unmarshal(%q) Msg = %q, want %q", tt.input, se.Msg, tt.wantMsg)
				}
				if se.Col != tt.wantCol {
					t.Errorf("Unmarshal(%q) Col = %d, want %d", tt.input, se.Col, tt.wantCol)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("Unmarshal(%q) did not return within 5s: tokenizer hang on bare CR", tt.input)
			}
		})
	}
}

// TestDecoderCRLFStillValid guards that fixing the bare-CR case does not break
// legitimate CRLF newlines, which must continue to decode successfully.
func TestDecoderCRLFStillValid(t *testing.T) {
	t.Parallel()

	var out map[string]any
	if err := Unmarshal([]byte("a=1\r\nb=2\r\n"), &out); err != nil {
		t.Fatalf("Unmarshal CRLF doc = %v, want nil", err)
	}
	if got := out["a"]; got != int64(1) {
		t.Errorf("out[a] = %v (%T), want int64(1)", got, got)
	}
	if got := out["b"]; got != int64(2) {
		t.Errorf("out[b] = %v (%T), want int64(2)", got, got)
	}
}

// classifyBareValueOld is a verbatim copy of the pre-optimization
// classifyBareValue. It exists solely so TestClassifyBareValueEquivalence can
// prove the first-byte-dispatch rewrite is behaviorally identical. It must not
// be edited: it is the oracle. It reuses the same package-level helpers as the
// production classifier, so any change to those helpers is exercised by both.
func classifyBareValueOld(raw []byte) (TokenKind, tokenScalar, string) {
	switch {
	case looksLikeDatetime(raw):
		return TokenKindValueDatetime, tokenScalar{}, ""
	case bytes.ContainsAny(raw, "= "):
		return TokenKindInvalid, tokenScalar{}, "unexpected = in value"
	case bytes.Equal(raw, trueLiteral) || bytes.Equal(raw, falseLiteral):
		return TokenKindValueBool, tokenScalar{}, ""
	case isSpecialFloatBytes(raw):
		f, _ := parseSpecialFloatLiteral(raw)
		return TokenKindValueFloat, tokenScalar{bits: math.Float64bits(f), kind: tokenScalarFloat}, ""
	case isIntCandidateBytes(raw):
		if hasCapitalNumericPrefixBytes(raw) {
			return TokenKindInvalid, tokenScalar{}, "malformed value"
		}
		i, err := parseIntegerLiteral(raw)
		if err != nil {
			return TokenKindInvalid, tokenScalar{}, "malformed value"
		}
		return TokenKindValueInteger, tokenScalar{bits: uint64(i), kind: tokenScalarInteger}, "" //nolint:gosec // G115: i is an int64 stored as raw bits; round-tripped by scalarIntegerValue.
	case isFloatCandidateBytes(raw):
		f, err := parseFloatLiteral(raw)
		if err != nil {
			return TokenKindInvalid, tokenScalar{}, "malformed float"
		}
		return TokenKindValueFloat, tokenScalar{bits: math.Float64bits(f), kind: tokenScalarFloat}, ""
	case containsMalformedBareValueByte(raw):
		return TokenKindInvalid, tokenScalar{}, "malformed value"
	default:
		return TokenKindInvalid, tokenScalar{}, "malformed value"
	}
}

// classifyEqual reports whether the new classifier agrees with the oracle on
// kind, scalar payload (compared by bits, so NaN patterns must match exactly),
// and error message.
func classifyEqual(raw []byte) (wantKind, gotKind TokenKind, wantScalar, gotScalar tokenScalar, wantMsg, gotMsg string, ok bool) {
	wantKind, wantScalar, wantMsg = classifyBareValueOld(raw)
	gotKind, gotScalar, gotMsg = classifyBareValue(raw)
	ok = wantKind == gotKind && wantScalar == gotScalar && wantMsg == gotMsg
	return wantKind, gotKind, wantScalar, gotScalar, wantMsg, gotMsg, ok
}

func assertClassifyEquivalent(t *testing.T, raw []byte) {
	t.Helper()
	wantKind, gotKind, wantScalar, gotScalar, wantMsg, gotMsg, ok := classifyEqual(raw)
	if !ok {
		t.Errorf("classifyBareValue(%q) diverged from oracle:\n old: kind=%v scalar=%+v msg=%q\n new: kind=%v scalar=%+v msg=%q",
			raw, wantKind, wantScalar, wantMsg, gotKind, gotScalar, gotMsg)
	}
}

// TestClassifyBareValueEquivalence is the non-negotiable differential test: for
// a large, deliberately adversarial corpus, the rewritten classifier must
// return identical (kind, scalar, message) triples to the verbatim oracle.
func TestClassifyBareValueEquivalence(t *testing.T) {
	seen := make(map[string]struct{})
	check := func(s string) {
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		assertClassifyEquivalent(t, []byte(s))
	}

	// nil and empty are distinct call shapes worth covering explicitly.
	assertClassifyEquivalent(t, nil)
	check("")

	// Curated cases spanning every branch and the boundaries the rewrite
	// reasons about: bases, signs, underscores, leading zeros, special floats
	// and their near-misses, bools and near-misses, datetimes (valid and not),
	// and malformed tokens carrying '=' or ' '.
	curated := []string{
		// bools and near-misses.
		"true", "false", "True", "FALSE", "tru", "truex", "falsey", "t", "f",
		"+true", "true ", "false=", "truefalse",
		// special floats and near-misses.
		"inf", "+inf", "-inf", "nan", "+nan", "-nan",
		"Inf", "INF", "NaN", "NAN", "infi", "nani", "in", "na", "+in", "-na",
		"i", "n", "+i", "-n", "infinity", "+infx",
		// decimal integers.
		"0", "+0", "-0", "00", "01", "007", "10", "100", "123", "-123", "+123",
		"1_000", "1_000_000", "+1_2", "-1_2", "1_2_3", "0_0", "0_", "_0",
		"1__2", "1_", "_1", "12_", "1_2_", "9223372036854775807",
		"9223372036854775808", "-9223372036854775808", "-9223372036854775809",
		// hex / octal / binary.
		"0x", "0X", "0x0", "0x1", "0xa", "0xA", "0xf", "0xF", "0xff", "0xFF",
		"0xF_F", "0xdead_beef", "0x_1", "0x1_", "0xg", "0xG", "0X1", "0Xff",
		"0o", "0O", "0o0", "0o7", "0o17", "0o777", "0o8", "0o_7", "0o7_", "0O7",
		"0b", "0B", "0b0", "0b1", "0b1010", "0b2", "0b_1", "0b1_", "0B1",
		"+0x1", "-0x1", "+0b1", "-0o7",
		// floats.
		"0.0", "1.0", "0.5", ".5", "1.", "3.14", "+3.14", "-3.14", "10.01",
		"1_0.0_1", "1.2.3", "1..2", "..", ".", "1e0", "1e10", "1E10", "1e+10",
		"1e-10", "1.5e3", "1.5E-3", "1e", "1e+", "1e-", "1_e3", "1e_3", ".e1",
		"0e0", "6.02e23", "9_224.61", "1.0e_1", "e5", "E5", "1.e3", "+.5", "-.5",
		// datetimes (valid and invalid shapes).
		"1979-05-27", "1979-05-27T07:32:00", "1979-05-27T07:32:00Z",
		"1979-05-27T00:32:00.999999-07:00", "1979-05-27 07:32:00", "07:32:00",
		"07:32:00.999999", "00:00", "00:00:00", "1979-05-27x", "1979-13-40",
		"2020-01-01 notatime", "0000-00-00", "0000-00-00T00:00:00Z",
		"1979-05-27T07:32", "24:00:00", "1979-5-7",
		// malformed with '=' / ' ' and other junk.
		"1=2", "a=b", "=", "= ", "x=", " ", "  ", "foo bar", "1 2",
		"true false", "hello", "forty-two", "0xoops", ": :", "1:2", "1:2:3",
		"+-1", "--1", "++1", "1-2", "1+2", "abc", "_", "e", "E", ":", "::",
		"+", "-", ".", "+.", "-.", "0x=", "1_=", "nan ", "inf=",
	}
	for _, s := range curated {
		check(s)
	}

	// Combinatorial numerics: every sign against a spread of integer and float
	// bodies, so signed/unsigned base and underscore edges are all crossed.
	signs := []string{"", "+", "-"}
	bodies := []string{
		"0", "1", "7", "8", "10", "123", "1_2", "1_2_3", "12_34", "0_0", "00",
		"01", "007", "1__2", "1_", "_1",
		"0x0", "0x1", "0xa", "0xA", "0xf", "0xff", "0xF_F", "0xdead_beef",
		"0x_1", "0x1_", "0xg", "0X1", "0o0", "0o7", "0o10", "0o8", "0o_7",
		"0O7", "0b0", "0b1", "0b1010", "0b2", "0b_1", "0B1",
		"0.0", "1.0", "0.5", ".5", "1.", "3.14", "10.01", "1_0.0_1", "1.2.3",
		"1e0", "1e10", "1E10", "1e+10", "1e-10", "1.5e3", "1.5E-3", "1e", "1e+",
		"1e-", "1_e3", "1e_3", ".e1", "0e0", "6.02e23",
	}
	for _, sign := range signs {
		for _, body := range bodies {
			check(sign + body)
		}
	}

	// Exhaustive short strings over an alphabet rich in TOML value syntax
	// (digits, signs, radix markers, exponent letters, separators, and the
	// leading letters of true/false/inf/nan). Lengths 1..3 fully enumerate the
	// first-byte dispatch boundaries; this is where an off-by-one in the
	// dispatch would surface.
	fullAlphabet := []byte("0178 9+-._eExXoObBAg:=afint")
	enumerate(fullAlphabet, 3, check)

	// Length-4 enumeration over a numeric-focused alphabet reaches signed
	// special floats, radix literals, and exponent forms that need four bytes.
	numAlphabet := []byte("019+-._eEx:o")
	enumerate(numAlphabet, 4, check)

	t.Logf("checked %d distinct inputs", len(seen))
}

// enumerate invokes fn for every non-empty string of length 1..maxLen over
// alphabet. Each candidate is materialized independently, so no aliasing can
// corrupt the emitted values.
func enumerate(alphabet []byte, maxLen int, fn func(string)) {
	var rec func(prefix []byte)
	rec = func(prefix []byte) {
		if len(prefix) > 0 {
			fn(string(prefix))
		}
		if len(prefix) == maxLen {
			return
		}
		for _, c := range alphabet {
			next := make([]byte, len(prefix)+1)
			copy(next, prefix)
			next[len(prefix)] = c
			rec(next)
		}
	}
	rec(nil)
}

// TestClassifyBareValueGolden pins the intended classification of
// representative inputs. The differential test proves new == old, but cannot
// catch a defect present in both; these goldens guard the absolute behavior.
func TestClassifyBareValueGolden(t *testing.T) {
	tests := map[string]struct {
		input    string
		wantKind TokenKind
		wantMsg  string
	}{
		"bool true":             {input: "true", wantKind: TokenKindValueBool},
		"bool false":            {input: "false", wantKind: TokenKindValueBool},
		"int zero":              {input: "0", wantKind: TokenKindValueInteger},
		"int positive sign":     {input: "+0", wantKind: TokenKindValueInteger},
		"int negative sign":     {input: "-0", wantKind: TokenKindValueInteger},
		"int decimal":           {input: "123", wantKind: TokenKindValueInteger},
		"int underscores":       {input: "1_000", wantKind: TokenKindValueInteger},
		"int hex":               {input: "0x1F", wantKind: TokenKindValueInteger},
		"int octal":             {input: "0o17", wantKind: TokenKindValueInteger},
		"int binary":            {input: "0b101", wantKind: TokenKindValueInteger},
		"float point":           {input: "3.14", wantKind: TokenKindValueFloat},
		"float exp":             {input: "1e10", wantKind: TokenKindValueFloat},
		"float inf":             {input: "inf", wantKind: TokenKindValueFloat},
		"float pos inf":         {input: "+inf", wantKind: TokenKindValueFloat},
		"float neg inf":         {input: "-inf", wantKind: TokenKindValueFloat},
		"float nan":             {input: "nan", wantKind: TokenKindValueFloat},
		"datetime date":         {input: "1979-05-27", wantKind: TokenKindValueDatetime},
		"datetime offset":       {input: "1979-05-27T07:32:00Z", wantKind: TokenKindValueDatetime},
		"datetime local time":   {input: "07:32:00", wantKind: TokenKindValueDatetime},
		"equals sign":           {input: "1=2", wantKind: TokenKindInvalid, wantMsg: "unexpected = in value"},
		"embedded space":        {input: "foo bar", wantKind: TokenKindInvalid, wantMsg: "unexpected = in value"},
		"capital hex prefix":    {input: "0X1", wantKind: TokenKindInvalid, wantMsg: "malformed value"},
		"double dotted":         {input: "1.2.3", wantKind: TokenKindInvalid, wantMsg: "malformed value"},
		"incomplete exponent":   {input: "1e", wantKind: TokenKindInvalid, wantMsg: "malformed float"},
		"leading exponent":      {input: "e5", wantKind: TokenKindInvalid, wantMsg: "malformed float"},
		"leading exponent dot":  {input: "E.01", wantKind: TokenKindInvalid, wantMsg: "malformed float"},
		"bool near miss":        {input: "truex", wantKind: TokenKindInvalid, wantMsg: "malformed value"},
		"bare radix marker":     {input: "0x", wantKind: TokenKindInvalid, wantMsg: "malformed value"},
		"double underscore":     {input: "1__2", wantKind: TokenKindInvalid, wantMsg: "malformed value"},
		"leading underscore":    {input: "_1", wantKind: TokenKindInvalid, wantMsg: "malformed value"},
		"empty":                 {input: "", wantKind: TokenKindInvalid, wantMsg: "malformed value"},
		"special near miss inf": {input: "infi", wantKind: TokenKindInvalid, wantMsg: "malformed value"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			gotKind, _, gotMsg := classifyBareValue([]byte(tc.input))
			if gotKind != tc.wantKind {
				t.Errorf("classifyBareValue(%q) kind = %v, want %v", tc.input, gotKind, tc.wantKind)
			}
			if gotMsg != tc.wantMsg {
				t.Errorf("classifyBareValue(%q) msg = %q, want %q", tc.input, gotMsg, tc.wantMsg)
			}
		})
	}
}

// TestClassifyBareValueScalar checks the scalar payload is carried through
// intact for known integer and float inputs.
func TestClassifyBareValueScalar(t *testing.T) {
	t.Run("integer bits", func(t *testing.T) {
		kind, scalar, msg := classifyBareValue([]byte("42"))
		if kind != TokenKindValueInteger || msg != "" {
			t.Fatalf("classifyBareValue(42) = kind %v msg %q, want integer", kind, msg)
		}
		if scalar.kind != tokenScalarInteger || scalar.bits != 42 {
			t.Errorf("scalar = %+v, want bits=42 kind=integer", scalar)
		}
	})

	t.Run("negative integer bits", func(t *testing.T) {
		kind, scalar, msg := classifyBareValue([]byte("-1"))
		if kind != TokenKindValueInteger || msg != "" {
			t.Fatalf("classifyBareValue(-1) = kind %v msg %q, want integer", kind, msg)
		}
		if scalar.kind != tokenScalarInteger || scalar.bits != uint64(0xFFFFFFFFFFFFFFFF) {
			t.Errorf("scalar = %+v, want bits=^0 kind=integer", scalar)
		}
	})

	t.Run("float bits", func(t *testing.T) {
		kind, scalar, msg := classifyBareValue([]byte("3.5"))
		if kind != TokenKindValueFloat || msg != "" {
			t.Fatalf("classifyBareValue(3.5) = kind %v msg %q, want float", kind, msg)
		}
		if scalar.kind != tokenScalarFloat || scalar.bits != math.Float64bits(3.5) {
			t.Errorf("scalar = %+v, want bits=%d kind=float", scalar, math.Float64bits(3.5))
		}
	})
}

const (
	propertyCases    = 20_000
	propertyMaxLen   = 1024
	propertySeedPath = "testdata/property_seed.txt"
)

func loadPropertySeed(tb testing.TB) uint64 {
	tb.Helper()
	f, err := os.Open(propertySeedPath)
	if err != nil {
		tb.Fatalf("open %s: %v", propertySeedPath, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			tb.Fatalf("close %s: %v", propertySeedPath, err)
		}
	}()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		v, err := strconv.ParseUint(line, 10, 64)
		if err != nil {
			tb.Fatalf("parse seed %q: %v", line, err)
		}
		return v
	}
	if err := sc.Err(); err != nil {
		tb.Fatalf("scan %s: %v", propertySeedPath, err)
	}
	tb.Fatalf("no seed value in %s", propertySeedPath)
	return 0
}

func newPropertyRand(seed uint64, label string) *rand.Rand {
	var labelHash uint64 = 0xcbf29ce484222325
	for _, c := range []byte(label) {
		labelHash ^= uint64(c)
		labelHash *= 0x100000001b3
	}
	return rand.New(rand.NewPCG(seed, labelHash))
}

func runDecoderParityProperty(t *testing.T, label string, data []byte) {
	t.Helper()
	gotTokens, gotErr := readAllTokens(NewDecoderBytes(data))
	wantTokens, wantErr := readAllTokens(NewDecoder(strings.NewReader(string(data))))

	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("%s error mismatch: bytes=%v reader=%v input=%x", label, gotErr, wantErr, data)
	}
	if gotErr != nil && wantErr != nil && gotErr.Error() != wantErr.Error() {
		t.Fatalf("%s error text mismatch: bytes=%v reader=%v input=%x", label, gotErr, wantErr, data)
	}
	if len(gotTokens) != len(wantTokens) {
		t.Fatalf("%s token count mismatch: bytes=%d reader=%d input=%x", label, len(gotTokens), len(wantTokens), data)
	}
	for i := range gotTokens {
		if gotTokens[i].Kind != wantTokens[i].Kind {
			t.Fatalf("%s token[%d] kind mismatch: bytes=%q reader=%q input=%x", label, i, gotTokens[i].Kind, wantTokens[i].Kind, data)
		}
		if !slices.Equal(gotTokens[i].Bytes, wantTokens[i].Bytes) {
			t.Fatalf("%s token[%d] bytes mismatch: bytes=%x reader=%x input=%x", label, i, gotTokens[i].Bytes, wantTokens[i].Bytes, data)
		}
		if gotTokens[i].Offset != wantTokens[i].Offset {
			t.Fatalf("%s token[%d] offset mismatch: bytes=%d reader=%d input=%x", label, i, gotTokens[i].Offset, wantTokens[i].Offset, data)
		}
	}
}

func runDecoderTokenStreamInvariant(t *testing.T, label string, data []byte) {
	t.Helper()
	runDecoderParityProperty(t, label, data)
	wantTokens, wantErr := readAllTokens(NewDecoder(strings.NewReader(string(data))))
	gotTokens, gotErr := readAllTokens(NewDecoderBytes(data))
	assertTokenStreamInvariants(t, label, data, gotTokens, gotErr)
	assertTokenStreamInvariants(t, label, data, wantTokens, wantErr)
}

func assertTokenStreamInvariants(t *testing.T, label string, data []byte, tokens []Token, err error) {
	t.Helper()
	if err != nil {
		return
	}
	prevOffset := -1
	for i, tok := range tokens {
		if tok.Offset < 0 || tok.Offset > len(data) {
			t.Fatalf("%s token[%d] invalid offset: %d input_len=%d input=%x", label, i, tok.Offset, len(data), data)
		}
		if i > 0 && tok.Offset <= prevOffset {
			t.Fatalf("%s token[%d] offset non-increasing: prev=%d cur=%d input=%x", label, i, prevOffset, tok.Offset, data)
		}
		if len(tok.Bytes) == 0 {
			t.Fatalf("%s token[%d] has empty bytes: offset=%d input=%x", label, i, tok.Offset, data)
		}
		if end := tok.Offset + len(tok.Bytes); end > len(data) {
			t.Fatalf("%s token[%d] span exceeds input: span=[%d,%d) input_len=%d input=%x", label, i, tok.Offset, end, len(data), data)
		}
		prevOffset = tok.Offset
	}
}

func TestDirectDecodeParity(t *testing.T) {
	t.Run("duration from integer", func(t *testing.T) {
		type config struct {
			Value time.Duration `toml:"value"`
		}
		got := runDirectGenericDecodeParity[config](t, "value = 5400000000000\n")
		if got.Value != 90*time.Minute {
			t.Fatalf("Value = %v, want %v", got.Value, 90*time.Minute)
		}
	})

	t.Run("int array", func(t *testing.T) {
		type config struct {
			Value [3]int `toml:"value"`
		}
		got := runDirectGenericDecodeParity[config](t, "value = [1, 2, 3]\n")
		if got.Value != [3]int{1, 2, 3} {
			t.Fatalf("Value = %#v, want %#v", got.Value, [3]int{1, 2, 3})
		}
	})

	t.Run("float slice", func(t *testing.T) {
		type config struct {
			Value []float64 `toml:"value"`
		}
		got := runDirectGenericDecodeParity[config](t, "value = [1, 2.5, 3]\n")
		want := []float64{1, 2.5, 3}
		if !reflect.DeepEqual(got.Value, want) {
			t.Fatalf("Value = %#v, want %#v", got.Value, want)
		}
	})

	t.Run("string array", func(t *testing.T) {
		type config struct {
			Value [2]string `toml:"value"`
		}
		got := runDirectGenericDecodeParity[config](t, `value = ["alpha", 'beta']`+"\n")
		if got.Value != [2]string{"alpha", "beta"} {
			t.Fatalf("Value = %#v, want %#v", got.Value, [2]string{"alpha", "beta"})
		}
	})

	t.Run("bytes from basic string", func(t *testing.T) {
		got := runDirectDecodeBytes(t, `value = "abc"`+"\n")
		if !bytes.Equal(got, []byte("abc")) {
			t.Fatalf("Value = %q, want %q", got, []byte("abc"))
		}
	})

	t.Run("bytes from escaped basic string", func(t *testing.T) {
		got := runDirectDecodeBytes(t, `value = "a\nb"`+"\n")
		if !bytes.Equal(got, []byte("a\nb")) {
			t.Fatalf("Value = %q, want %q", got, []byte("a\nb"))
		}
	})

	t.Run("bytes from literal string", func(t *testing.T) {
		got := runDirectDecodeBytes(t, `value = 'a\nb'`+"\n")
		if !bytes.Equal(got, []byte(`a\nb`)) {
			t.Fatalf("Value = %q, want %q", got, []byte(`a\nb`))
		}
	})

	t.Run("duration from string", func(t *testing.T) {
		type config struct {
			Value time.Duration `toml:"value"`
		}
		var got config
		if err := Unmarshal([]byte(`value = "1h30m"`+"\n"), &got); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if got.Value != 90*time.Minute {
			t.Fatalf("Value = %v, want %v", got.Value, 90*time.Minute)
		}
	})
}

func TestDirectDecodeBytesCopyStringsOption(t *testing.T) {
	t.Run("aliases input by default", func(t *testing.T) {
		data := []byte(`value = "abc"` + "\n")
		value := runDirectDecodeBytesFromData(t, data)
		value[0] = 'z'
		if !bytes.Contains(data, []byte(`"zbc"`)) {
			t.Fatalf("decoded bytes did not alias input: data = %q", data)
		}
	})

	t.Run("copies when requested", func(t *testing.T) {
		data := []byte(`value = "abc"` + "\n")
		value := runDirectDecodeBytesFromData(t, data, WithCopiedStrings())
		value[0] = 'z'
		if bytes.Contains(data, []byte(`"zbc"`)) {
			t.Fatalf("decoded bytes mutated input despite WithCopiedStrings: data = %q", data)
		}
	})
}

func runDirectGenericDecodeParity[T any](t *testing.T, input string) T {
	t.Helper()

	data := []byte(input)
	var direct T
	if err := Unmarshal(data, &direct); err != nil {
		t.Fatalf("direct Unmarshal(%q) error = %v", input, err)
	}

	var generic T
	withDirectEligibility(t, reflect.TypeFor[T](), false, func() {
		if err := Unmarshal(data, &generic); err != nil {
			t.Fatalf("generic Unmarshal(%q) error = %v", input, err)
		}
	})

	if !reflect.DeepEqual(direct, generic) {
		t.Fatalf("direct/generic mismatch: direct=%#v generic=%#v", direct, generic)
	}
	return direct
}

func runDirectDecodeBytes(t *testing.T, input string) []byte {
	t.Helper()
	return runDirectDecodeBytesFromData(t, []byte(input))
}

func runDirectDecodeBytesFromData(t *testing.T, data []byte, opts ...Option) []byte {
	t.Helper()
	type config struct {
		Value []byte `toml:"value"`
	}
	var got config
	if err := (UnmarshalOptions{DecoderOptions: opts}).Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", data, err)
	}
	return got.Value
}

func withDirectEligibility(t *testing.T, typ reflect.Type, eligible bool, f func()) {
	t.Helper()
	old, hadOld := directStructEligibilityCache.Load(typ)
	directStructEligibilityCache.Store(typ, eligible)
	defer func() {
		if hadOld {
			directStructEligibilityCache.Store(typ, old)
			return
		}
		directStructEligibilityCache.Delete(typ)
	}()
	f()
}

func TestProperty_DecoderConstructorParity(t *testing.T) {
	t.Parallel()
	seed := loadPropertySeed(t)
	r := newPropertyRand(seed, "DecoderConstructorParity")
	buf := make([]byte, propertyMaxLen)
	for range propertyCases {
		l := r.IntN(propertyMaxLen + 1)
		for i := 0; i < l; {
			w := r.Uint64()
			for k := 0; k < 8 && i < l; k, i = k+1, i+1 {
				buf[i] = byte(w >> (8 * k))
			}
		}
		runDecoderParityProperty(t, "DecoderConstructorParity", buf[:l])
	}
}

func TestProperty_DecoderCorpusParity(t *testing.T) {
	t.Parallel()
	corpus := decoderCorpusFiles(t)
	for _, rel := range corpus {
		body := mustReadRepoFile(t, rel)
		runDecoderParityProperty(t, rel, body)
		runDecoderTokenStreamInvariant(t, rel, body)
	}
}

func decoderCorpusFiles(tb testing.TB) []string {
	tb.Helper()
	root := mustRepoPath(tb, "testdata")
	var files []string
	for _, rel := range []string{"corpus"} {
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			tb.Fatalf("os.ReadDir(%s) error = %v", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			files = append(files, filepath.ToSlash(filepath.Join("testdata", rel, entry.Name())))
		}
	}
	slices.Sort(files)
	return files
}
