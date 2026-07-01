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
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFacadeUnmarshalScalarsTablesAndArrays(t *testing.T) {
	t.Parallel()

	type Server struct {
		Host  string
		Ports []int
	}
	type Config struct {
		Name    string
		Active  bool
		Score   float64
		When    time.Time
		Server  Server
		Labels  map[string]string
		Plugins []struct {
			Name    string
			Enabled bool
		}
		Any any `toml:"misc"`
	}

	input := []byte(`name = "demo"
active = true
score = 1.5
when = 2026-05-17T03:04:05Z
misc = [1, 2, 3]

[server]
host = "127.0.0.1"
ports = [80, 443]

[labels]
env = "test"

[[plugins]]
name = "cache"
enabled = true

[[plugins]]
name = "trace"
enabled = false
`)
	var got Config
	if err := Unmarshal(input, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got.Name != "demo" || !got.Active || got.Score != 1.5 {
		t.Fatalf("scalar fields = %#v", got)
	}
	if got.When.Format(time.RFC3339) != "2026-05-17T03:04:05Z" {
		t.Fatalf("When = %s", got.When.Format(time.RFC3339Nano))
	}
	if got.Server.Host != "127.0.0.1" || len(got.Server.Ports) != 2 || got.Server.Ports[1] != 443 {
		t.Fatalf("Server = %#v", got.Server)
	}
	if got.Labels["env"] != "test" {
		t.Fatalf("Labels = %#v", got.Labels)
	}
	if len(got.Plugins) != 2 || got.Plugins[0].Name != "cache" || got.Plugins[1].Enabled {
		t.Fatalf("Plugins = %#v", got.Plugins)
	}
	if _, ok := got.Any.([]any); !ok {
		t.Fatalf("Any = %T(%#v), want []any", got.Any, got.Any)
	}
}

func TestFacadeUnmarshalMapStringAnyPreservesPublicContainers(t *testing.T) {
	t.Parallel()

	input := []byte(`title = "demo"
[server]
host = "127.0.0.1"
[server.tls]
enabled = true
certs = [{ name = "primary" }, { name = "backup" }]

[inline]
value = { nested = { ok = true } }
`)

	var got map[string]any
	if err := Unmarshal(input, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	server, ok := got["server"].(map[string]any)
	if !ok {
		t.Fatalf("server = %T(%#v), want map[string]any", got["server"], got["server"])
	}
	tls, ok := server["tls"].(map[string]any)
	if !ok {
		t.Fatalf("server.tls = %T(%#v), want map[string]any", server["tls"], server["tls"])
	}
	certs, ok := tls["certs"].([]any)
	if !ok {
		t.Fatalf("server.tls.certs = %T(%#v), want []any", tls["certs"], tls["certs"])
	}
	for i, item := range certs {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("server.tls.certs[%d] = %T(%#v), want map[string]any", i, item, item)
		}
		if _, ok := entry["name"].(string); !ok {
			t.Fatalf("server.tls.certs[%d].name = %T(%#v), want string", i, entry["name"], entry["name"])
		}
	}
	inline, ok := got["inline"].(map[string]any)
	if !ok {
		t.Fatalf("inline = %T(%#v), want map[string]any", got["inline"], got["inline"])
	}
	value, ok := inline["value"].(map[string]any)
	if !ok {
		t.Fatalf("inline.value = %T(%#v), want map[string]any", inline["value"], inline["value"])
	}
	nested, ok := value["nested"].(map[string]any)
	if !ok {
		t.Fatalf("inline.value.nested = %T(%#v), want map[string]any", value["nested"], value["nested"])
	}
	if got, ok := nested["ok"].(bool); !ok || !got {
		t.Fatalf("inline.value.nested.ok = %T(%#v), want true bool", nested["ok"], nested["ok"])
	}
}

func TestFacadeMarshalStructTagsOmitZeroAndRoundTrip(t *testing.T) {
	t.Parallel()

	type Server struct {
		Host string
		Port int
	}
	type Config struct {
		Name   string `toml:"name"`
		Empty  string `toml:"empty,omitzero"`
		Server Server `toml:"server"`
		Tags   []string
	}
	body, err := Marshal(Config{Name: "demo", Server: Server{Host: "localhost", Port: 8080}, Tags: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"name = \"demo\"",
		"Tags = [\"a\", \"b\"]",
		"[server]",
		"Host = \"localhost\"",
		"Port = 8080",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Marshal output missing %q\n%s", want, text)
		}
	}
	if strings.Contains(text, "empty") {
		t.Fatalf("Marshal output included omitzero field\n%s", text)
	}

	var round Config
	if err := Unmarshal(body, &round); err != nil {
		t.Fatalf("roundtrip Unmarshal() error = %v\n%s", err, text)
	}
	if round.Name != "demo" || round.Server.Port != 8080 || len(round.Tags) != 2 {
		t.Fatalf("roundtrip = %#v", round)
	}
}

func TestMarshalDirectCompatibilityOutputShape(t *testing.T) {
	t.Parallel()

	type item struct {
		Name string
	}
	type table struct {
		B int
	}
	type config struct {
		Z     string
		A     string
		Table table
		Items []item
	}

	body, err := Marshal(config{
		Z:     "z",
		A:     "a",
		Table: table{B: 1},
		Items: []item{{Name: "one"}, {Name: "two"}},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = `A = "a"
Z = "z"

[[Items]]
Name = "one"

[[Items]]
Name = "two"

[Table]
B = 1
`
	if got := string(body); got != want {
		t.Fatalf("Marshal() output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarshalDirectCompatibilityQuotedMapKeys(t *testing.T) {
	t.Parallel()

	body, err := Marshal(map[string]any{
		"sp ace":       true,
		"alpha":        int64(1),
		"key.with.dot": "v",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = `alpha = 1
"key.with.dot" = "v"
"sp ace" = true
`
	if got := string(body); got != want {
		t.Fatalf("Marshal() output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarshalFloatWholeNumbersRemainTOMLFloats(t *testing.T) {
	t.Parallel()

	body, err := Marshal(map[string]any{
		"exponent": 5e22,
		"neg_zero": math.Copysign(0, -1),
		"whole":    1000.0,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = `exponent = 5e+22
neg_zero = -0.0
whole = 1000.0
`
	if got := string(body); got != want {
		t.Fatalf("Marshal() output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestSortedStringKeysReusableOrdering(t *testing.T) {
	first := sortedStringKeys(map[string]any{
		"z": true,
		"a": true,
		"m": true,
	})
	if got, want := strings.Join(first, ","), "a,m,z"; got != want {
		t.Fatalf("first sortedStringKeys() = %q, want %q", got, want)
	}
	recycleStringKeys(first)

	second := sortedStringKeys(map[string]any{
		"b": true,
		"a": true,
	})
	defer recycleStringKeys(second)
	if got, want := strings.Join(second, ","), "a,b"; got != want {
		t.Fatalf("second sortedStringKeys() = %q, want %q", got, want)
	}
}

func TestEncoderBytesBufferMatchesMarshal(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string
		Port int
	}
	value := config{Name: "demo", Port: 8080}

	want, err := Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got bytes.Buffer
	if err := NewEncoder(&got).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if got.String() != string(want) {
		t.Fatalf("Encode() = %q, want %q", got.String(), string(want))
	}
}

func TestMarshalDirectCompatibilityDuplicateTagOverwrite(t *testing.T) {
	t.Parallel()

	type config struct {
		First  string `toml:"name"`
		Second string `toml:"name"`
		Empty  string `toml:"name,omitzero"`
	}

	body, err := Marshal(config{First: "first", Second: "second"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = "name = \"second\"\n"
	if got := string(body); got != want {
		t.Fatalf("Marshal() output mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestMarshalDirectCompatibilityScalarSpecialsAndErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value   any
		want    string
		wantErr string
	}{
		"success: datetime values": {
			value: struct {
				When time.Time
				Date LocalDate
			}{
				When: time.Date(2026, 5, 17, 3, 4, 5, 0, time.UTC),
				Date: LocalDate{Year: 2026, Month: 5, Day: 17},
			},
			want: "Date = 2026-05-17\nWhen = 2026-05-17T03:04:05Z\n",
		},
		"success: signed nan": {
			value: struct {
				Value float64
			}{Value: math.Float64frombits(0xfff8000000000000)},
			want: "Value = -nan\n",
		},
		"success: nil interface is omitted": {
			value: struct {
				Value any
			}{},
			want: "",
		},
		"error: unsupported channel": {
			value: struct {
				Ch chan int
			}{Ch: make(chan int)},
			wantErr: "toml: unsupported type chan int",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body, err := Marshal(tc.value)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("Marshal() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if got := string(body); got != tc.want {
				t.Fatalf("Marshal() output mismatch:\ngot:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}

	textValue := struct {
		Value customText
	}{Value: "encoded"}
	body, err := Marshal(textValue)
	if err != nil {
		t.Fatalf("Marshal(TextMarshaler) error = %v", err)
	}
	if got, want := string(body), "Value = \"encoded\"\n"; got != want {
		t.Fatalf("Marshal(TextMarshaler) = %q, want %q", got, want)
	}
}

func TestFacadeRejectsOmitEmptyAsTypedError(t *testing.T) {
	t.Parallel()

	type Bad struct {
		Name string `toml:"name,omitempty"`
	}

	_, err := Marshal(Bad{Name: "x"})
	var tagErr *TagOptionError
	if !errors.As(err, &tagErr) || tagErr.Option != "omitempty" {
		t.Fatalf("Marshal error = %T(%v), want TagOptionError option=omitempty", err, err)
	}

	var dst Bad
	err = Unmarshal([]byte("name = \"x\"\n"), &dst)
	if !errors.As(err, &tagErr) || tagErr.Option != "omitempty" {
		t.Fatalf("Unmarshal error = %T(%v), want TagOptionError option=omitempty", err, err)
	}
}

func TestFacadeCustomHooks(t *testing.T) {
	t.Parallel()

	var encoded bytes.Buffer
	if err := NewEncoder(&encoded).Encode(customFacade{}); err != nil {
		t.Fatalf("Encode(custom) error = %v", err)
	}
	if got := encoded.String(); got != "name = \"custom\"\n" {
		t.Fatalf("encoded custom = %q", got)
	}

	var dst customFacade
	if err := Unmarshal([]byte("name = \"ignored\"\n"), &dst); err != nil {
		t.Fatalf("Unmarshal(custom) error = %v", err)
	}
	if !dst.decoded {
		t.Fatalf("custom UnmarshalTOMLFrom was not called")
	}
}

func TestFacadeUnmarshalReportsNestedMismatchPath(t *testing.T) {
	t.Parallel()

	type item struct {
		Count int
	}
	type config struct {
		Items []item `toml:"items"`
	}

	input := []byte(`[[items]]
count = 1

[[items]]
count = "bad"
`)
	var dst config
	err := Unmarshal(input, &dst)
	var mismatch *TypeMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Unmarshal() error = %T(%v), want TypeMismatchError", err, err)
	}
	if mismatch.Path != "items[1].count" {
		t.Fatalf("TypeMismatchError.Path = %q, want %q", mismatch.Path, "items[1].count")
	}
}

func TestFacadeUnmarshalDirectNestedArrayTables(t *testing.T) {
	t.Parallel()

	type config struct {
		Fruit []struct {
			Name     string
			Physical struct {
				Color string
				Shape string
			}
			Variety []struct {
				Name string
			}
		}
	}
	input := []byte(`[[fruit]]
name = "apple"

[fruit.physical]
color = "red"
shape = "round"

[[fruit.variety]]
name = "red delicious"

[[fruit.variety]]
name = "granny smith"

[[fruit]]
name = "banana"

[[fruit.variety]]
name = "plantain"
`)
	var dst config
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(dst.Fruit) != 2 {
		t.Fatalf("len(Fruit) = %d, want 2", len(dst.Fruit))
	}
	if got, want := dst.Fruit[0].Name, "apple"; got != want {
		t.Fatalf("Fruit[0].Name = %q, want %q", got, want)
	}
	if got, want := dst.Fruit[0].Physical.Color, "red"; got != want {
		t.Fatalf("Fruit[0].Physical.Color = %q, want %q", got, want)
	}
	if len(dst.Fruit[0].Variety) != 2 {
		t.Fatalf("len(Fruit[0].Variety) = %d, want 2", len(dst.Fruit[0].Variety))
	}
	if got, want := dst.Fruit[0].Variety[1].Name, "granny smith"; got != want {
		t.Fatalf("Fruit[0].Variety[1].Name = %q, want %q", got, want)
	}
	if len(dst.Fruit[1].Variety) != 1 {
		t.Fatalf("len(Fruit[1].Variety) = %d, want 1", len(dst.Fruit[1].Variety))
	}
	if got, want := dst.Fruit[1].Variety[0].Name, "plantain"; got != want {
		t.Fatalf("Fruit[1].Variety[0].Name = %q, want %q", got, want)
	}
}

func TestFacadeUnmarshalEmptyMapTakesDecodedEntries(t *testing.T) {
	t.Parallel()

	dst := map[string]any{}
	if err := Unmarshal([]byte("name = \"demo\"\ncount = 2\n"), &dst); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got, want := dst["name"], "demo"; got != want {
		t.Fatalf("name = %v, want %v", got, want)
	}
	if got, want := dst["count"], int64(2); got != want {
		t.Fatalf("count = %v, want %v", got, want)
	}
}

func TestFacadeUnmarshalMapUsesPublicContainers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
	}{
		"nested tables and inline tables": {
			input: `title = "TOML"

[owner]
name = "Tom"
inline = { child = { answer = 42 }, items = [ { name = "first" }, { name = "second" } ] }

[[fruit]]
name = "apple"

[fruit.physical]
color = "red"

[[fruit.variety]]
name = "red delicious"

[[fruit.variety]]
name = "granny smith"
`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dst := map[string]any{}
			if err := Unmarshal([]byte(tc.input), &dst); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			assertNoDocumentMap(t, "dst", dst)

			var iface any
			if err := Unmarshal([]byte(tc.input), &iface); err != nil {
				t.Fatalf("Unmarshal() into interface error = %v", err)
			}
			assertNoDocumentMap(t, "iface", iface)
		})
	}
}

func TestFacadeUnmarshalStructWithNestedMapUsesGenericPath(t *testing.T) {
	t.Parallel()

	type config struct {
		Labels map[string]map[string]string
	}

	var dst config
	if err := Unmarshal([]byte("[labels.prod]\nname = \"api\"\n"), &dst); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got, want := dst.Labels["prod"]["name"], "api"; got != want {
		t.Fatalf("Labels[prod][name] = %q, want %q", got, want)
	}
}

func TestFacadeUnmarshalMapKeepsExistingEntries(t *testing.T) {
	t.Parallel()

	dst := map[string]any{"existing": "keep"}
	if err := Unmarshal([]byte(`name = "demo"`), &dst); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if got, want := dst["existing"], "keep"; got != want {
		t.Fatalf("existing entry = %v, want %v", got, want)
	}
	if got, want := dst["name"], "demo"; got != want {
		t.Fatalf("decoded entry = %v, want %v", got, want)
	}
}

func TestFacadeUnmarshalMapRecursivelyConvertsPublicContainers(t *testing.T) {
	t.Parallel()

	dst := map[string]any{}
	input := []byte(`title = "demo"
[owner]
name = "alice"
aliases = ["a", "b"]

[owner.meta]
enabled = true
labels = ["x", "y"]
`)
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	owner, ok := dst["owner"].(map[string]any)
	if !ok {
		t.Fatalf("owner = %T(%#v), want map[string]any", dst["owner"], dst["owner"])
	}
	if _, ok := owner["__toml_internal__"]; ok {
		t.Fatalf("owner contains unexpected internal sentinel: %#v", owner)
	}
	if got, want := owner["name"], "alice"; got != want {
		t.Fatalf("owner.name = %#v, want %#v", got, want)
	}
	aliases, ok := owner["aliases"].([]any)
	if !ok {
		t.Fatalf("owner.aliases = %T(%#v), want []any", owner["aliases"], owner["aliases"])
	}
	if got, want := len(aliases), 2; got != want {
		t.Fatalf("len(owner.aliases) = %d, want %d", got, want)
	}
	meta, ok := owner["meta"].(map[string]any)
	if !ok {
		t.Fatalf("owner.meta = %T(%#v), want map[string]any", owner["meta"], owner["meta"])
	}
	if got, want := meta["enabled"], true; got != want {
		t.Fatalf("owner.meta.enabled = %#v, want %#v", got, want)
	}
	labels, ok := meta["labels"].([]any)
	if !ok {
		t.Fatalf("owner.meta.labels = %T(%#v), want []any", meta["labels"], meta["labels"])
	}
	if got, want := len(labels), 2; got != want {
		t.Fatalf("len(owner.meta.labels) = %d, want %d", got, want)
	}
}

func TestMarshalWriteQuotedStringMatchesStrconvQuote(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"success: ascii":           "simple text",
		"success: quote backslash": "quote \" and slash \\",
		"success: control":         "line\n tab\t nul\x00",
		"success: unicode":         "snowman ☃ and 日本語",
		"success: invalid utf8":    string([]byte{'o', 'k', 0xff}),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			writeQuotedString(&buf, input)
			if got, want := buf.String(), strconv.Quote(input); got != want {
				t.Fatalf("writeQuotedString(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestMarshalASCIIQuoteEscapeIndex(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input string
		want  int
	}{
		"success: plain ascii":      {input: "plain-ascii_123", want: -1},
		"success: quote":            {input: `needs"quote`, want: 5},
		"success: backslash":        {input: `needs\\slash`, want: 5},
		"success: control fallback": {input: "line\n", want: quoteFallback},
		"success: unicode fallback": {input: "snowman ☃", want: quoteFallback},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := asciiQuoteEscapeIndex(tc.input); got != tc.want {
				t.Fatalf("asciiQuoteEscapeIndex(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestMarshalWriteSpecialValueHandlesOnlySpecialTypes(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	ok, err := writeSpecialValue(&buf, reflect.ValueOf("plain"))
	if err != nil {
		t.Fatalf("writeSpecialValue(plain string) error = %v", err)
	}
	if ok || buf.Len() != 0 {
		t.Fatalf("writeSpecialValue(plain string) = ok:%v buf:%q, want no special handling", ok, buf.String())
	}

	date := LocalDate{Year: 2026, Month: 6, Day: 10}
	ok, err = writeSpecialValue(&buf, reflect.ValueOf(date))
	if err != nil {
		t.Fatalf("writeSpecialValue(LocalDate) error = %v", err)
	}
	if !ok || buf.String() != "2026-06-10" {
		t.Fatalf("writeSpecialValue(LocalDate) = ok:%v buf:%q, want date text", ok, buf.String())
	}

	buf.Reset()
	ok, err = writeSpecialValue(&buf, reflect.ValueOf(customText("needs\nquote")))
	if err != nil {
		t.Fatalf("writeSpecialValue(TextMarshaler) error = %v", err)
	}
	if !ok || buf.String() != strconv.Quote("needs\nquote") {
		t.Fatalf("writeSpecialValue(TextMarshaler) = ok:%v buf:%q, want quoted text", ok, buf.String())
	}
}

func TestMarshalSizeHintPositiveAndBounded(t *testing.T) {
	t.Parallel()

	type packageEntry struct {
		Name    string
		Version string
	}
	value := struct {
		Version int
		Package []packageEntry
	}{
		Version: 3,
		Package: []packageEntry{
			{Name: "alpha", Version: "1.0.0"},
			{Name: "beta", Version: "2.0.0"},
		},
	}
	body, err := Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(cargo-like value) error = %v", err)
	}
	if got := marshalSizeHint(value); got < len(body) {
		t.Fatalf("marshalSizeHint(cargo-like value) = %d, want >= output len %d", got, len(body))
	}

	huge := map[string]any{"payload": strings.Repeat("x", maxMarshalSizeHint+1)}
	if got := marshalSizeHint(huge); got != maxMarshalSizeHint {
		t.Fatalf("marshalSizeHint(huge) = %d, want cap %d", got, maxMarshalSizeHint)
	}
}

type customFacade struct{ decoded bool }

type customText string

func (c customText) MarshalText() ([]byte, error) {
	return []byte(c), nil
}

func (customFacade) MarshalTOMLTo(enc *Encoder) error {
	_, err := enc.Write([]byte("name = \"custom\"\n"))
	return err
}

func (c *customFacade) UnmarshalTOMLFrom(dec *Decoder) error {
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

func assertNoDocumentMap(t *testing.T, path string, value any) {
	t.Helper()
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			assertNoDocumentMap(t, path+"."+key, child)
		}
	case []any:
		for i, child := range v {
			assertNoDocumentMap(t, fmt.Sprintf("%s[%d]", path, i), child)
		}
	}
}
