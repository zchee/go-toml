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
	"bytes"
	"math"
	"testing"
)

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
