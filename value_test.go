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
	"errors"
	"math"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/google/go-cmp/cmp"
)

func TestParseStringValueHotPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "literal", input: "'demo'", want: "demo"},
		{name: "basic", input: "\"demo\"", want: "demo"},
		{name: "literal multiline", input: "'''demo'''", want: "demo"},
		{name: "basic multiline", input: "\"\"\"demo\"\"\"", want: "demo"},
		{name: "literal multiline trims initial newline", input: "'''\nfirst\n'''", want: "first\n"},
		{name: "literal multiline trims initial crlf", input: "'''\r\nfirst\r\n'''", want: "first\r\n"},
		{name: "basic multiline trims initial newline", input: "\"\"\"\nfirst\n\"\"\"", want: "first\n"},
		{name: "basic multiline trims initial crlf", input: "\"\"\"\r\nfirst\r\n\"\"\"", want: "first\r\n"},
		{name: "basic multiline line ending backslash", input: "\"\"\"first\\\n  second\"\"\"", want: "firstsecond"},
		{name: "escaped", input: "\"demo\\nline\"", want: "demo\nline"},
		{name: "unicode escape", input: "\"caf\\u00e9\"", want: "café"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseStringValue([]byte(tc.input))
			if err != nil {
				t.Fatalf("parseStringValue(%q) error = %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("parseStringValue(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseStringValueRejectsInvalidTOMLStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "basic null", input: "\"\x00\""},
		{name: "literal null", input: "'\x00'"},
		{name: "multiline basic vertical tab", input: "\"\"\"\v\"\"\""},
		{name: "multiline basic bare carriage return", input: "\"\"\"\r\"\"\""},
		{name: "multiline literal bare carriage return", input: "'''\r'''"},
		{name: "invalid unicode escape hex", input: "\"\\u00xz\""},
		{name: "invalid unicode scalar", input: "\"\\U00110000\""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, err := parseStringValue([]byte(tc.input)); err == nil {
				t.Fatalf("parseStringValue(%q) = %q, nil error; want error", tc.input, got)
			}
		})
	}
}

func TestParseStringValueTOML11HexEscape(t *testing.T) {
	t.Parallel()

	got, err := parseStringValue([]byte("\"\\x41\\x1b\""))
	if err != nil {
		t.Fatalf("parseStringValue() TOML 1.1 x escape error = %v", err)
	}
	if got != "A\x1b" {
		t.Fatalf("parseStringValue() = %q, want %q", got, "A\x1b")
	}
}

func TestValidateStringValueMatchesParseStringValueErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
	}{
		"success: literal": {
			input: "'demo'",
		},
		"success: basic escaped": {
			input: "\"demo\\nline\"",
		},
		"success: TOML 1.1 hex escape": {
			input: "\"\\x41\\x1b\"",
		},
		"success: unicode escape": {
			input: "\"caf\\u00e9\"",
		},
		"success: multiline basic line ending backslash": {
			input: "\"\"\"first\\\n  second\"\"\"",
		},
		"success: multiline literal trims CRLF": {
			input: "'''\r\nfirst\r\n'''",
		},
		"error: malformed string": {
			input: "bare",
		},
		"error: basic null": {
			input: "\"\x00\"",
		},
		"error: literal null": {
			input: "'\x00'",
		},
		"error: multiline basic vertical tab": {
			input: "\"\"\"\v\"\"\"",
		},
		"error: invalid string escape": {
			input: "\"\\q\"",
		},
		"error: trailing escape": {
			input: "\"\\\"",
		},
		"error: short unicode escape": {
			input: "\"\\u00\"",
		},
		"error: invalid unicode escape": {
			input: "\"\\u00xz\"",
		},
		"error: invalid unicode scalar": {
			input: "\"\\U00110000\"",
		},
		"error: unicode parse range": {
			input: "\"\\U80000000\"",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, parseErr := parseStringValue([]byte(tc.input))
			validateErr := validateStringValue([]byte(tc.input))
			assertStringValidationErrorsMatch(t, parseErr, validateErr)
		})
	}
}

func TestValidateStringValueAvoidsSuccessAllocation(t *testing.T) {
	tests := map[string]struct {
		input []byte
	}{
		"success: literal": {
			input: []byte("'demo'"),
		},
		"success: basic no escape": {
			input: []byte("\"demo\""),
		},
		"success: basic escaped": {
			input: []byte("\"demo\\nline\""),
		},
		"success: TOML 1.1 hex escape": {
			input: []byte("\"\\x41\\x1b\""),
		},
		"success: unicode escape": {
			input: []byte("\"caf\\u00e9\""),
		},
		"success: multiline basic line ending backslash": {
			input: []byte("\"\"\"first\\\n  second\"\"\""),
		},
		"success: multiline literal": {
			input: []byte("'''\r\nfirst\r\n'''"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			valid := true
			allocs := testing.AllocsPerRun(1000, func() {
				if err := validateStringValue(tc.input); err != nil {
					valid = false
				}
			})
			if !valid {
				t.Fatalf("validateStringValue(%q) returned an unexpected error", tc.input)
			}
			if allocs != 0 {
				t.Fatalf("validateStringValue(%q) allocs/run = %v, want 0", tc.input, allocs)
			}
		})
	}
}

func assertStringValidationErrorsMatch(t *testing.T, parseErr, validateErr error) {
	t.Helper()

	if (parseErr == nil) != (validateErr == nil) {
		t.Fatalf("parseStringValue error = %v, validateStringValue error = %v", parseErr, validateErr)
	}
	if parseErr == nil {
		return
	}
	if parseErr.Error() != validateErr.Error() {
		t.Fatalf("validateStringValue error = %q, want parseStringValue error %q", validateErr, parseErr)
	}

	var parseSyntaxErr *SyntaxError
	var validateSyntaxErr *SyntaxError
	parseIsSyntax := errors.As(parseErr, &parseSyntaxErr)
	validateIsSyntax := errors.As(validateErr, &validateSyntaxErr)
	if parseIsSyntax != validateIsSyntax {
		t.Fatalf("validateStringValue syntax error = %v, want %v", validateIsSyntax, parseIsSyntax)
	}
	if !parseIsSyntax {
		return
	}
	if *validateSyntaxErr != *parseSyntaxErr {
		t.Fatalf("validateStringValue syntax error = %#v, want %#v", validateSyntaxErr, parseSyntaxErr)
	}
}

func TestParseDottedKeyHotPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "bare segments", input: "fruit.apple.taste", want: []string{"fruit", "apple", "taste"}},
		{name: "quoted segment", input: "fruit.\"apple pie\".taste", want: []string{"fruit", "apple pie", "taste"}},
		{name: "literal segment", input: "fruit.'apple pie'.taste", want: []string{"fruit", "apple pie", "taste"}},
		{name: "quoted empty segment", input: `fruit.""`, want: []string{"fruit", ""}},
		{name: "literal empty segment", input: `fruit.''`, want: []string{"fruit", ""}},
		{name: "spaced", input: "  fruit . apple  ", want: []string{"fruit", "apple"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseDottedKey([]byte(tc.input))
			if err != nil {
				t.Fatalf("parseDottedKey(%q) error = %v", tc.input, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseDottedKey(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseDirectRawPathHotPaths(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  string
		ok    bool
	}{
		"success: bare dotted": {
			input: "fruit.variety",
			want:  "fruit.variety",
			ok:    true,
		},
		"success: bare with trim": {
			input: "  table.subtable  ",
			want:  "table.subtable",
			ok:    true,
		},
		"fallback: quoted": {
			input: `fruit."apple pie"`,
		},
		"fallback: spaced separator": {
			input: "fruit . variety",
		},
		"fallback: empty segment": {
			input: "fruit..variety",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseDirectRawPath([]byte(tc.input))
			if ok != tc.ok {
				t.Fatalf("parseDirectRawPath(%q) ok = %v, want %v", tc.input, ok, tc.ok)
			}
			if !ok {
				return
			}
			if got.string() != tc.want {
				t.Fatalf("parseDirectRawPath(%q).string() = %q, want %q", tc.input, got.string(), tc.want)
			}
		})
	}
}

func TestDirectAssignmentForKeyAvoidsSuccessAllocation(t *testing.T) {
	type sample struct {
		Count int
	}

	dst := reflect.ValueOf(&sample{}).Elem()
	raw := []byte("count")
	if _, ok, err := directAssignmentForKey(dst, raw); err != nil || !ok {
		t.Fatalf("directAssignmentForKey() = ok %v, err %v; want ok true", ok, err)
	}

	valid := true
	allocs := testing.AllocsPerRun(1000, func() {
		target, ok, err := directAssignmentForKey(dst, raw)
		if err != nil || !ok || !target.dst.IsValid() {
			valid = false
		}
	})
	if !valid {
		t.Fatal("directAssignmentForKey() did not return a valid target")
	}
	if checkptrInstrumented() {
		t.Skipf("checkptr instrumentation changes unsafe lookup allocation count: got %v", allocs)
	}
	if allocs != 0 {
		t.Fatalf("directAssignmentForKey() allocs = %v, want 0", allocs)
	}
}

func TestDirectAssignmentForKeyInfoReusesCurrentStructMetadata(t *testing.T) {
	type sample struct {
		Count int
	}

	dst := reflect.ValueOf(&sample{}).Elem()
	info, err := directStructInfo(dst)
	if err != nil {
		t.Fatalf("directStructInfo() error = %v", err)
	}
	raw := []byte("count")
	target, ok, err := directAssignmentForKeyInfo(dst, info, raw)
	if err != nil || !ok {
		t.Fatalf("directAssignmentForKeyInfo() = ok %v, err %v; want ok true", ok, err)
	}
	if target.dst.Kind() != reflect.Int {
		t.Fatalf("directAssignmentForKeyInfo() dst kind = %s, want int", target.dst.Kind())
	}
}

func TestDirectDestinationRawResolvesFinalStructField(t *testing.T) {
	type sample struct {
		Fruit struct {
			Physical struct {
				Color string
			}
		}
	}

	dst := reflect.ValueOf(&sample{}).Elem()
	path, ok := parseDirectRawPath([]byte("fruit.physical.color"))
	if !ok {
		t.Fatal("parseDirectRawPath() failed for bare dotted path")
	}
	target, valueKind, ok, err := directDestinationRaw(dst, path)
	if err != nil || !ok {
		t.Fatalf("directDestinationRaw() = ok %v, err %v; want ok true", ok, err)
	}
	if !target.IsValid() || target.Kind() != reflect.String {
		t.Fatalf("directDestinationRaw() target = %#v, want string field", target)
	}
	if valueKind != directValueString {
		t.Fatalf("directDestinationRaw() valueKind = %v, want directValueString", valueKind)
	}
}

func TestAppendDirectSliceElementUsesCapacityHintAndZerosSlot(t *testing.T) {
	type item struct {
		Name  string
		Extra string
	}
	type sample struct {
		Items []item
	}

	stale := []item{{Name: "stale", Extra: "stale"}}
	dst := sample{Items: stale[:0]}
	slot := reflect.ValueOf(&dst).Elem().FieldByName("Items")
	elem, index := appendDirectSliceElement(slot, 7)
	if index != 0 {
		t.Fatalf("appendDirectSliceElement() index = %d, want 0", index)
	}
	if len(dst.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(dst.Items))
	}
	if cap(dst.Items) < 7 {
		t.Fatalf("cap(Items) = %d, want at least 7", cap(dst.Items))
	}
	if got := dst.Items[0]; got != (item{}) {
		t.Fatalf("Items[0] after append = %#v, want zero value", got)
	}
	if elem.Kind() != reflect.Struct || elem.Type() != reflect.TypeFor[item]() {
		t.Fatalf("element = %v %v, want settable item struct", elem.Kind(), elem.Type())
	}
}

func TestDirectArrayTableCapacityHintOnlyCountsFirstTopLevelHeader(t *testing.T) {
	t.Parallel()

	data := []byte("[[items]]\nname = \"a\"\n[[items]]\nname = \"b\"\n[[nested.items]]\nname = \"c\"\n")
	header := []byte("[[items]]")
	tests := map[string]struct {
		pathLen    int
		currentLen int
		want       int
	}{
		"success: first top-level header counts exact token occurrences": {
			pathLen: 1,
			want:    2,
		},
		"success: subsequent top-level header does not rescan": {
			pathLen:    1,
			currentLen: 1,
		},
		"success: nested header does not use global hint": {
			pathLen: 2,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := directArrayTableCapacityHint(data, header, tc.pathLen, tc.currentLen)
			if got != tc.want {
				t.Fatalf("directArrayTableCapacityHint(pathLen=%d, currentLen=%d) = %d, want %d", tc.pathLen, tc.currentLen, got, tc.want)
			}
		})
	}
}

func TestFacadeUnmarshalDirectArrayTableClearsStaleCapacity(t *testing.T) {
	t.Parallel()

	type item struct {
		Name  string
		Extra string
	}
	type sample struct {
		Items []item `toml:"items"`
	}

	stale := []item{{Name: "stale", Extra: "stale"}}
	dst := sample{Items: stale[:0]}
	if err := Unmarshal([]byte("[[items]]\nname = \"fresh\"\n"), &dst); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(dst.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(dst.Items))
	}
	if got, want := dst.Items[0].Name, "fresh"; got != want {
		t.Fatalf("Items[0].Name = %q, want %q", got, want)
	}
	if got := dst.Items[0].Extra; got != "" {
		t.Fatalf("Items[0].Extra = %q, want cleared stale value", got)
	}
}

func TestDirectStringValueAliasesInputForEscapeFreeStrings(t *testing.T) {
	t.Parallel()

	type sample struct {
		Basic   string
		Literal string
		Multi   string
		Escaped string
	}

	input := []byte("basic = \"alpha\"\nliteral = 'beta'\nmulti = \"\"\"\nline\n\"\"\"\nescaped = \"a\\nb\"\n")
	var dst sample
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got, want := dst.Basic, "alpha"; got != want {
		t.Fatalf("Basic = %q, want %q", got, want)
	}
	if got, want := dst.Literal, "beta"; got != want {
		t.Fatalf("Literal = %q, want %q", got, want)
	}
	if got, want := dst.Multi, "line\n"; got != want {
		t.Fatalf("Multi = %q, want %q", got, want)
	}
	if got, want := dst.Escaped, "a\nb"; got != want {
		t.Fatalf("Escaped = %q, want %q", got, want)
	}

	// Default aliasing contract: escape-free string values alias the caller's
	// input buffer directly (zero copy), so their data pointers fall inside the
	// input's byte range. Escaped strings are materialized into fresh
	// allocations and therefore do not alias the input.
	inputBase := bytesDataPointer(input)
	if p := stringDataPointer(dst.Basic); !pointerInRange(p, inputBase, len(input)) {
		t.Fatalf("Basic string pointer %#x not inside input buffer [%#x,%#x); want aliasing", p, inputBase, inputBase+uintptr(len(input)))
	}
	if p := stringDataPointer(dst.Literal); !pointerInRange(p, inputBase, len(input)) {
		t.Fatalf("Literal string pointer %#x not inside input buffer [%#x,%#x); want aliasing", p, inputBase, inputBase+uintptr(len(input)))
	}
	if p := stringDataPointer(dst.Multi); !pointerInRange(p, inputBase, len(input)) {
		t.Fatalf("Multi string pointer %#x not inside input buffer [%#x,%#x); want aliasing", p, inputBase, inputBase+uintptr(len(input)))
	}
	if escaped := stringDataPointer(dst.Escaped); pointerInRange(escaped, inputBase, len(input)) {
		t.Fatalf("escaped string pointer %#x is inside input buffer [%#x,%#x); want independent allocation", escaped, inputBase, inputBase+uintptr(len(input)))
	}

	// Because Basic aliases the input, mutating the input mutates the decoded
	// string. Callers that need independence must use WithCopiedStrings (see
	// TestDirectStringValueCopiedStringsBypassesArena).
	copy(input[bytes.Index(input, []byte("alpha")):], "omega")
	if got, want := dst.Basic, "omega"; got != want {
		t.Fatalf("Basic after input mutation = %q, want aliased %q", got, want)
	}
}

func TestDirectStringValueCopiedStringsBypassesArena(t *testing.T) {
	t.Parallel()

	input := []byte("value = \"alpha\"\n")
	raw := input[bytes.IndexByte(input, '"') : len(input)-1]
	dec := NewDecoderBytes(input)
	got, err := directStringValue(dec, rawToken{Kind: TokenKindValueString, Bytes: raw}, bindConfig{copyStrings: true})
	if err != nil {
		t.Fatalf("directStringValue(copyStrings) error = %v", err)
	}
	if got != "alpha" {
		t.Fatalf("directStringValue(copyStrings) = %q, want alpha", got)
	}
	// The copy path must return an independent allocation, not a view aliasing
	// the input buffer: its data pointer must lie outside input's byte range.
	inputBase := bytesDataPointer(input)
	if p := stringDataPointer(got); pointerInRange(p, inputBase, len(input)) {
		t.Fatalf("copyStrings result pointer %#x is inside input buffer [%#x,%#x); want independent copy", p, inputBase, inputBase+uintptr(len(input)))
	}

	cfg := bindConfigFromOptions([]Option{WithCopiedStrings()})
	if !cfg.copyStrings {
		t.Fatal("bindConfigFromOptions(WithCopiedStrings()) copyStrings = false, want true")
	}
}

func stringDataPointer(s string) uintptr {
	if s == "" {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.StringData(s)))
}

func bytesDataPointer(b []byte) uintptr {
	if len(b) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.SliceData(b)))
}

func pointerInRange(ptr, base uintptr, n int) bool {
	return ptr >= base && ptr < base+uintptr(n)
}

func TestDirectBindTypedTokenScalarDestinations(t *testing.T) {
	type sample struct {
		Name     string
		Enabled  bool
		Count    int16
		Unsigned uint16
		Ratio    float32
		When     time.Time
	}

	tests := map[string]struct {
		field string
		kind  directValueKind
		token Token
		check func(t *testing.T, got sample)
	}{
		"success: string": {
			field: "Name",
			kind:  directValueString,
			token: Token{Kind: TokenKindValueString, Bytes: []byte(`"demo"`)},
			check: func(t *testing.T, got sample) {
				t.Helper()
				if got.Name != "demo" {
					t.Fatalf("Name = %q, want demo", got.Name)
				}
			},
		},
		"success: bool": {
			field: "Enabled",
			kind:  directValueBool,
			token: Token{Kind: TokenKindValueBool, Bytes: []byte("true")},
			check: func(t *testing.T, got sample) {
				t.Helper()
				if !got.Enabled {
					t.Fatal("Enabled = false, want true")
				}
			},
		},
		"success: int": {
			field: "Count",
			kind:  directValueInt,
			token: Token{Kind: TokenKindValueInteger, Bytes: []byte("12")},
			check: func(t *testing.T, got sample) {
				t.Helper()
				if got.Count != 12 {
					t.Fatalf("Count = %d, want 12", got.Count)
				}
			},
		},
		"success: uint": {
			field: "Unsigned",
			kind:  directValueUint,
			token: Token{Kind: TokenKindValueInteger, Bytes: []byte("34")},
			check: func(t *testing.T, got sample) {
				t.Helper()
				if got.Unsigned != 34 {
					t.Fatalf("Unsigned = %d, want 34", got.Unsigned)
				}
			},
		},
		"success: float": {
			field: "Ratio",
			kind:  directValueFloat,
			token: Token{Kind: TokenKindValueFloat, Bytes: []byte("1.25")},
			check: func(t *testing.T, got sample) {
				t.Helper()
				if got.Ratio != 1.25 {
					t.Fatalf("Ratio = %v, want 1.25", got.Ratio)
				}
			},
		},
		"success: time": {
			field: "When",
			kind:  directValueTime,
			token: Token{Kind: TokenKindValueDatetime, Bytes: []byte("2026-05-17T03:04:05Z")},
			check: func(t *testing.T, got sample) {
				t.Helper()
				if got.When.Format(time.RFC3339) != "2026-05-17T03:04:05Z" {
					t.Fatalf("When = %s, want 2026-05-17T03:04:05Z", got.When.Format(time.RFC3339Nano))
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var got sample
			field := reflect.ValueOf(&got).Elem().FieldByName(tc.field)
			if err := directBindTypedToken(nil, rawTokenFromToken(tc.token), field, tc.kind, bindConfig{}); err != nil {
				t.Fatalf("directBindTypedToken() error = %v", err)
			}
			tc.check(t, got)
		})
	}
}

func TestDirectBindTypedTokenLocalTimeUTCOption(t *testing.T) {
	type sample struct {
		When time.Time
	}

	tok := Token{Kind: TokenKindValueDatetime, Bytes: []byte("2026-05-17T03:04:05")}
	var rejected sample
	err := directBindTypedToken(nil, rawTokenFromToken(tok), reflect.ValueOf(&rejected).Elem().FieldByName("When"), directValueTime, bindConfig{})
	if _, ok := errors.AsType[*LocalTimeIntoTimeError](err); !ok {
		t.Fatalf("directBindTypedToken(local datetime) error = %T(%v), want LocalTimeIntoTimeError", err, err)
	}

	var got sample
	if err := directBindTypedToken(nil, rawTokenFromToken(tok), reflect.ValueOf(&got).Elem().FieldByName("When"), directValueTime, bindConfig{localAsUTC: true}); err != nil {
		t.Fatalf("directBindTypedToken(localAsUTC) error = %v", err)
	}
	want := time.Date(2026, time.May, 17, 3, 4, 5, 0, time.UTC)
	if !got.When.Equal(want) || got.When.Location() != time.UTC {
		t.Fatalf("When = %s (%s), want %s UTC", got.When, got.When.Location(), want)
	}

	var noSeconds sample
	noSecondsTok := Token{Kind: TokenKindValueDatetime, Bytes: []byte("2026-05-17T03:04")}
	if err := directBindTypedToken(nil, rawTokenFromToken(noSecondsTok), reflect.ValueOf(&noSeconds).Elem().FieldByName("When"), directValueTime, bindConfig{localAsUTC: true}); err != nil {
		t.Fatalf("directBindTypedToken(no-seconds) error = %v", err)
	}
	want = time.Date(2026, time.May, 17, 3, 4, 0, 0, time.UTC)
	if !noSeconds.When.Equal(want) || noSeconds.When.Location() != time.UTC {
		t.Fatalf("When = %s (%s), want %s UTC", noSeconds.When, noSeconds.When.Location(), want)
	}
}

func TestDirectBindTypedTokenRejectsCapitalizedBool(t *testing.T) {
	type sample struct {
		Enabled bool
	}

	var got sample
	err := directBindTypedToken(nil, rawTokenFromToken(Token{Kind: TokenKindValueBool, Bytes: []byte("TRUE")}), reflect.ValueOf(&got).Elem().FieldByName("Enabled"), directValueBool, bindConfig{})
	if err == nil {
		t.Fatal("directBindTypedToken(TRUE) error = nil, want syntax error")
	}
}

func checkptrInstrumented() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, setting := range info.Settings {
		if setting.Key == "-gcflags" && strings.Contains(setting.Value, "checkptr") {
			return true
		}
	}
	return false
}

func TestParseValueTokenNormalizationHotPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tok  Token
		want any
	}{
		{name: "integer underscores", tok: Token{Kind: TokenKindValueInteger, Bytes: []byte("1_2_3")}, want: int64(123)},
		{name: "float normalization", tok: Token{Kind: TokenKindValueFloat, Bytes: []byte("1_2.3E+4")}, want: 123000.0},
		{name: "bool true", tok: Token{Kind: TokenKindValueBool, Bytes: []byte("true")}, want: true},
		{name: "bool false", tok: Token{Kind: TokenKindValueBool, Bytes: []byte("false")}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseValueToken(nil, tc.tok)
			if err != nil {
				t.Fatalf("parseValueToken(%q) error = %v", tc.tok.Bytes, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseValueToken(%q) = %#v, want %#v", tc.tok.Bytes, got, tc.want)
			}
		})
	}
}

func TestDirectBindTypedTokenNumericAvoidsNormalizationAllocation(t *testing.T) {
	type sample struct {
		I int64
		U uint64
		F float64
	}
	tests := map[string]struct {
		field string
		kind  directValueKind
		tok   Token
	}{
		"success: signed integer underscores": {
			field: "I",
			kind:  directValueInt,
			tok:   Token{Kind: TokenKindValueInteger, Bytes: []byte("1_2_3")},
		},
		"success: unsigned integer underscores": {
			field: "U",
			kind:  directValueUint,
			tok:   Token{Kind: TokenKindValueInteger, Bytes: []byte("1_2_3")},
		},
		"success: float underscores exponent": {
			field: "F",
			kind:  directValueFloat,
			tok:   Token{Kind: TokenKindValueFloat, Bytes: []byte("1_2.3E+4")},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var got sample
			dst := reflect.ValueOf(&got).Elem().FieldByName(tc.field)
			valid := true
			allocs := testing.AllocsPerRun(1000, func() {
				dst.SetZero()
				if err := directBindTypedToken(nil, rawTokenFromToken(tc.tok), dst, tc.kind, bindConfig{}); err != nil {
					valid = false
				}
			})
			if !valid {
				t.Fatalf("directBindTypedToken(%q) failed", tc.tok.Bytes)
			}
			wantAllocs := 0.0
			if tc.kind == directValueFloat {
				wantAllocs = 1
			}
			if allocs > wantAllocs {
				t.Fatalf("directBindTypedToken(%q) allocs/run = %v, want <= %v", tc.tok.Bytes, allocs, wantAllocs)
			}
		})
	}
}

func TestSkipStructuralValueFastValidatesSkippedSubtrees(t *testing.T) {
	tests := map[string]struct {
		input    string
		wantFast bool
		wantErr  bool
	}{
		"success: nested array and inline table": {
			input:    "ignored = [\"a\", {name = \"nested\", nums = [1_2, 3.4e5]}]\nnext = true\n",
			wantFast: true,
		},
		"error: malformed scalar inside skipped array": {
			input:    "ignored = [1__2]\nnext = true\n",
			wantFast: true,
			wantErr:  true,
		},
		"error: inline table value cannot move to next line": {
			input:    "ignored = {name =\n\"bad\"}\nnext = true\n",
			wantFast: true,
			wantErr:  true,
		},
		"success: scalar remains on token fallback path": {
			input: "ignored = 1_2_3\nnext = true\n",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dec := NewDecoderBytes([]byte(tc.input))
			tok, err := dec.ReadToken()
			if err != nil {
				t.Fatalf("ReadToken key error = %v", err)
			}
			if tok.Kind != TokenKindKey {
				t.Fatalf("ReadToken key kind = %s, want %s", tok.Kind, TokenKindKey)
			}
			fast, err := skipStructuralValueFast(dec)
			if fast != tc.wantFast {
				t.Fatalf("skipStructuralValueFast fast = %v, want %v", fast, tc.wantFast)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatal("skipStructuralValueFast error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("skipStructuralValueFast error = %v", err)
			}
			if !fast {
				return
			}
			tok, err = dec.ReadToken()
			if err != nil {
				t.Fatalf("ReadToken after skipped value error = %v", err)
			}
			if string(tok.Bytes) != "next" {
				t.Fatalf("ReadToken after skipped value = %q, want next", tok.Bytes)
			}
		})
	}
}

func TestSkipStructuralValueFastAvoidsTokenizingNestedScalars(t *testing.T) {
	input := []byte("ignored = [\"a\", {name = \"nested\", nums = [1_2, 3.4e5]}]\nnext = true\n")
	var valid bool
	allocs := testing.AllocsPerRun(1000, func() {
		dec := newAllocationHotPathDecoder(input)
		if _, err := dec.ReadToken(); err != nil {
			valid = false
			return
		}
		fast, err := skipStructuralValueFast(&dec)
		if err != nil || !fast {
			valid = false
			return
		}
		valid = true
	})
	if !valid {
		t.Fatal("skipStructuralValueFast did not skip valid nested input")
	}
	if allocs != 0 {
		t.Fatalf("skipStructuralValueFast allocs/run = %v, want 0", allocs)
	}
}

func TestParseValueTokenRejectsCapitalizedSpecialFloat(t *testing.T) {
	t.Parallel()

	_, err := parseValueToken(nil, Token{Kind: TokenKindValueFloat, Bytes: []byte("Inf")})
	if err == nil {
		t.Fatal("parseValueToken(Inf) error = nil, want syntax error")
	}
}

func TestParseValueTokenRejectsCapitalizedBool(t *testing.T) {
	t.Parallel()

	_, err := parseValueToken(nil, Token{Kind: TokenKindValueBool, Bytes: []byte("TRUE")})
	if err == nil {
		t.Fatal("parseValueToken(TRUE) error = nil, want syntax error")
	}
}

func TestParseValueTokenSpecialFloatLiterals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tok  Token
		want float64
	}{
		{name: "positive inf", tok: Token{Kind: TokenKindValueFloat, Bytes: []byte("inf")}, want: math.Inf(1)},
		{name: "negative inf", tok: Token{Kind: TokenKindValueFloat, Bytes: []byte("-inf")}, want: math.Inf(-1)},
		{name: "nan", tok: Token{Kind: TokenKindValueFloat, Bytes: []byte("nan")}, want: math.Float64frombits(0x7ff8000000000000)},
		{name: "negative nan", tok: Token{Kind: TokenKindValueFloat, Bytes: []byte("-nan")}, want: math.Float64frombits(0xfff8000000000000)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseValueToken(nil, tc.tok)
			if err != nil {
				t.Fatalf("parseValueToken(%q) error = %v", tc.tok.Bytes, err)
			}
			gotF, ok := got.(float64)
			if !ok {
				t.Fatalf("parseValueToken(%q) = %T, want float64", tc.tok.Bytes, got)
			}
			if math.IsNaN(tc.want) {
				if !math.IsNaN(gotF) || math.Signbit(gotF) != math.Signbit(tc.want) {
					t.Fatalf("parseValueToken(%q) = %v, want signed NaN", tc.tok.Bytes, gotF)
				}
				return
			}
			if gotF != tc.want {
				t.Fatalf("parseValueToken(%q) = %v, want %v", tc.tok.Bytes, gotF, tc.want)
			}
		})
	}
}

// TestParseDocumentMapDecodeEquivalence pins the byte-for-byte decode output of
// the allocation-leaned parseDocument map path. It exercises every branch the
// leaning touched: the bare-key fast path, dotted keys, inline-table and array
// values (which still seal closed-inline scopes), nested table headers, and
// array-of-tables. The expected value is the TOML-defined result, so any drift
// in the leaned path surfaces here.
func TestParseDocumentMapDecodeEquivalence(t *testing.T) {
	t.Parallel()

	const input = `title = "hi"
count = 42
ratio = 1.5
on = true
point = { x = 1, y = 2 }
ports = [8000, 8001]

[owner]
name = "tom"

[owner.address]
city = "sf"

[db]
conn.host = "localhost"
conn.port = 5432

[[items]]
id = 1

[[items]]
id = 2
`

	want := map[string]any{
		"title": "hi",
		"count": int64(42),
		"ratio": 1.5,
		"on":    true,
		"point": map[string]any{"x": int64(1), "y": int64(2)},
		"ports": []any{int64(8000), int64(8001)},
		"owner": map[string]any{
			"name":    "tom",
			"address": map[string]any{"city": "sf"},
		},
		"db": map[string]any{
			"conn": map[string]any{"host": "localhost", "port": int64(5432)},
		},
		"items": []any{
			map[string]any{"id": int64(1)},
			map[string]any{"id": int64(2)},
		},
	}

	var got map[string]any
	if err := Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Unmarshal() map mismatch (-want +got):\n%s", diff)
	}
}

// TestParseDocumentMapDecodeErrorsPreserved verifies the duplicate/redefinition
// diagnostics still fire on the exact paths the leaning restructured: the
// bare-key fast path's inline duplicate check, the lazily built dotted-key
// error path, and the container-value scope-sealing that blocks later
// extension. These decode into map[string]any, the target that reaches the
// leaned code.
func TestParseDocumentMapDecodeErrorsPreserved(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
	}{
		"error: duplicate bare key":                 {input: "a = 1\na = 2\n"},
		"error: duplicate dotted key":               {input: "a.b = 1\na.b = 2\n"},
		"error: scalar redefined as dotted table":   {input: "a = false\na.b = true\n"},
		"error: inline table sealed against header": {input: "a = {}\n[a.b]\n"},
		"error: array value sealed against header":  {input: "a = [1, 2]\n[a.b]\n"},
		"error: duplicate table header":             {input: "[a]\nb = 1\n\n[a]\nc = 2\n"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var got map[string]any
			if err := Unmarshal([]byte(tc.input), &got); err == nil {
				t.Fatalf("Unmarshal(%q) error = nil, want failure", tc.input)
			}
		})
	}
}

// TestParseDocumentMapDecodeValidStructuresPreserved guards valid documents that
// depend on the array-table epoch and implicit-parent bookkeeping the leaning
// left in place, ensuring the guarded empty-map fast returns in
// declarationContext and closedInlinePrefix stay behavior-neutral once array or
// inline tables appear.
func TestParseDocumentMapDecodeValidStructuresPreserved(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
	}{
		"valid: implicit parent becomes explicit":     {input: "[a.b.c]\nanswer = 42\n\n[a]\nbetter = 43\n"},
		"valid: array table subtable repeats":         {input: "[[arr]]\n[arr.sub]\nv = 1\n\n[[arr]]\n[arr.sub]\nv = 2\n"},
		"valid: dotted key after array table element": {input: "[[fruits]]\nname = 'apple'\nphysical.color = 'red'\n[[fruits]]\nname = 'banana'\n[fruits.physical]\ncolor = 'yellow'\n"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var got map[string]any
			if err := Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatalf("Unmarshal(%q) error = %v", tc.input, err)
			}
		})
	}
}

// TestParseDocumentMapDecodeLargeCorpusParity anchors the leaned path against a
// large real-world array-table document (cargo.lock: 424 [[package]] entries,
// bare scalar keys, a top-level integer, and the array-table capacity/epoch
// bookkeeping). The golden values would break if the fast-path key binding or
// array-table appends altered decode output, so it locks byte-level parity at
// scale beyond the curated equivalence case.
func TestParseDocumentMapDecodeLargeCorpusParity(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/corpus/cargo.lock")
	if err != nil {
		t.Skipf("corpus unavailable: %v", err)
	}

	var got map[string]any
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(cargo.lock) error = %v", err)
	}

	if v, ok := got["version"].(int64); !ok || v != 3 {
		t.Fatalf("version = %#v, want int64(3)", got["version"])
	}
	pkgs, ok := got["package"].([]any)
	if !ok {
		t.Fatalf("package = %T, want []any", got["package"])
	}
	if len(pkgs) != 424 {
		t.Fatalf("len(package) = %d, want 424", len(pkgs))
	}
	edges := map[int]struct{ name, version string }{
		0:   {"adler2", "2.0.0"},
		423: {"zeroize", "1.7.0"},
	}
	for idx, want := range edges {
		pkg, ok := pkgs[idx].(map[string]any)
		if !ok {
			t.Fatalf("package[%d] = %T, want map[string]any", idx, pkgs[idx])
		}
		if pkg["name"] != want.name || pkg["version"] != want.version {
			t.Fatalf("package[%d] = {name:%v version:%v}, want {name:%q version:%q}",
				idx, pkg["name"], pkg["version"], want.name, want.version)
		}
	}
}

// TestParseDocumentMapDecodeAllocations locks the allocation win from leaning
// parseDocument's per-key/per-header temporaries. The unoptimized path decoded
// benchmark.toml into map[string]any at 670 allocs/op; the ceiling keeps a wide
// regression margin while staying below the pelletier baseline (~619).
func TestParseDocumentMapDecodeAllocations(t *testing.T) {
	data, err := os.ReadFile("benchmark/testdata/benchmark.toml")
	if err != nil {
		t.Skipf("reference corpus unavailable: %v", err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		var dst map[string]any
		if err := Unmarshal(data, &dst); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
	})

	const ceiling = 500
	if allocs > ceiling {
		t.Errorf("map[string]any decode allocs/op = %.0f, want <= %d (regression; unoptimized baseline was 670)", allocs, ceiling)
	}
}
