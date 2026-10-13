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
	"errors"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"
)

func TestDirectBindSimpleAssignment(t *testing.T) {
	t.Parallel()
	type cfg struct {
		Name    string
		Count   int
		Active  bool
		Ratio   float64
		Unknown string // not in input
	}
	input := []byte(`
name = "demo"
count = 42
active = true
ratio = 3.5
skipped = "ignored"
`)
	var dst cfg
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "demo" || dst.Count != 42 || !dst.Active || dst.Ratio != 3.5 {
		t.Fatalf("got %+v", dst)
	}
}

func TestDirectBindSkipsUnknownStringArray(t *testing.T) {
	t.Parallel()
	type pkg struct {
		Name string
	}
	type root struct {
		Package []pkg `toml:"package"`
	}
	// dependencies is unknown and must be skipped without breaking the next package.
	input := []byte(`
[[package]]
name = "a"
dependencies = [
  "x",
  "y",
]

[[package]]
name = "b"
`)
	var dst root
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if len(dst.Package) != 2 || dst.Package[0].Name != "a" || dst.Package[1].Name != "b" {
		t.Fatalf("got %+v", dst.Package)
	}
}

func TestDirectBindStringSliceArray(t *testing.T) {
	t.Parallel()
	type pkg struct {
		Name         string
		Dependencies []string
	}
	type root struct {
		Package []pkg `toml:"package"`
	}
	input := []byte(`
[[package]]
name = "a"
dependencies = [
  "x",
  "y",
]
`)
	var dst root
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if len(dst.Package) != 1 || dst.Package[0].Name != "a" {
		t.Fatalf("got %+v", dst.Package)
	}
	if got := dst.Package[0].Dependencies; len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("deps = %#v", got)
	}
}

func TestDirectBindFallbackDoesNotCorruptState(t *testing.T) {
	t.Parallel()
	// Dotted key must fall back cleanly then still decode subsequent simple keys.
	type nested struct {
		Inner int `toml:"inner"`
	}
	type cfg struct {
		Outer nested `toml:"outer"`
		Name  string
	}
	input := []byte(`
outer.inner = 7
name = "ok"
`)
	var dst cfg
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Outer.Inner != 7 || dst.Name != "ok" {
		t.Fatalf("got %+v", dst)
	}
}

func TestDirectBindArrayTableHeader(t *testing.T) {
	t.Parallel()
	type item struct {
		N int
	}
	type cfg struct {
		Items []item `toml:"items"`
	}
	input := []byte(`
[[items]]
n = 1
[[items]]
n = 2
`)
	var dst cfg
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if len(dst.Items) != 2 || dst.Items[0].N != 1 || dst.Items[1].N != 2 {
		t.Fatalf("got %+v", dst.Items)
	}
}

func TestDirectBindMismatchPath(t *testing.T) {
	t.Parallel()
	type item struct {
		Count int
	}
	type config struct {
		Items []item `toml:"items"`
	}
	input := []byte(`
[[items]]
count = 1
[[items]]
count = "bad"
`)
	var dst config
	err := Unmarshal(input, &dst)
	var mismatch *TypeMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %T(%v)", err, err)
	}
	if mismatch.Path != "items[1].count" {
		t.Fatalf("path = %q", mismatch.Path)
	}
}

func TestDirectBindStringSliceEmptyAndTrailingComma(t *testing.T) {
	t.Parallel()
	type cfg struct {
		Deps []string `toml:"deps"`
		Name string
	}
	input := []byte(`
deps = [
  "a",
  "b",
]
name = "ok"
`)
	var dst cfg
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "ok" {
		t.Fatalf("name = %q", dst.Name)
	}
	if got := dst.Deps; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("deps = %#v", got)
	}

	var empty cfg
	if err := Unmarshal([]byte(`deps = []
name = "e"
`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Name != "e" || empty.Deps == nil || len(empty.Deps) != 0 {
		t.Fatalf("empty = %+v", empty)
	}
}

func TestDirectBindStringSliceWithComments(t *testing.T) {
	t.Parallel()
	type cfg struct {
		Deps []string `toml:"deps"`
		Next int
	}
	input := []byte(`
deps = [
  # leading
  "x",
  "y", # trailing comment
  # between
  "z",
]
next = 9
`)
	var dst cfg
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Next != 9 {
		t.Fatalf("next = %d", dst.Next)
	}
	if got := dst.Deps; len(got) != 3 || got[0] != "x" || got[1] != "y" || got[2] != "z" {
		t.Fatalf("deps = %#v", got)
	}
}

func TestDirectBindStringSliceComplexFallback(t *testing.T) {
	t.Parallel()
	// Non-string element forces generic array binder; subsequent keys must still work.
	type cfg struct {
		Nums []int `toml:"nums"`
		Name string
	}
	input := []byte(`
nums = [1, 2, 3]
name = "ok"
`)
	var dst cfg
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "ok" || len(dst.Nums) != 3 || dst.Nums[0] != 1 || dst.Nums[2] != 3 {
		t.Fatalf("got %+v", dst)
	}
}

func TestDirectBindStringSliceMixedElementFallback(t *testing.T) {
	t.Parallel()
	// []any with a non-string forces reset-to-'[' generic bind.
	type cfg struct {
		Items []any `toml:"items"`
		Name  string
	}
	input := []byte(`
items = ["a", 1]
name = "ok"
`)
	var dst cfg
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "ok" || len(dst.Items) != 2 {
		t.Fatalf("got %+v", dst)
	}
	if dst.Items[0] != "a" {
		t.Fatalf("items[0] = %#v", dst.Items[0])
	}
}

func TestDirectBindQuotedKeyFallback(t *testing.T) {
	t.Parallel()
	type cfg struct {
		Name  string `toml:"my-name"`
		Count int
	}
	input := []byte(`
"my-name" = "quoted"
count = 3
`)
	var dst cfg
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Name != "quoted" || dst.Count != 3 {
		t.Fatalf("got %+v", dst)
	}
}

func TestDirectBindUnknownArrayThenKnownFields(t *testing.T) {
	t.Parallel()
	type pkg struct {
		Name    string
		Version string
	}
	type root struct {
		Package []pkg `toml:"package"`
	}
	input := []byte(`
[[package]]
name = "a"
dependencies = [
  "x",
  "y",
]
version = "1.0"

[[package]]
name = "b"
dependencies = ["z"]
version = "2.0"
`)
	var dst root
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if len(dst.Package) != 2 {
		t.Fatalf("len = %d", len(dst.Package))
	}
	if dst.Package[0].Name != "a" || dst.Package[0].Version != "1.0" {
		t.Fatalf("pkg0 = %+v", dst.Package[0])
	}
	if dst.Package[1].Name != "b" || dst.Package[1].Version != "2.0" {
		t.Fatalf("pkg1 = %+v", dst.Package[1])
	}
}

func TestDirectBindKnownDepsThenNextPackage(t *testing.T) {
	t.Parallel()
	type pkg struct {
		Name         string
		Dependencies []string
	}
	type root struct {
		Package []pkg `toml:"package"`
	}
	input := []byte(`
[[package]]
name = "a"
dependencies = [
  "x",
  'y',
]

[[package]]
name = "b"
dependencies = []
`)
	var dst root
	if err := Unmarshal(input, &dst); err != nil {
		t.Fatal(err)
	}
	if len(dst.Package) != 2 {
		t.Fatalf("len = %d", len(dst.Package))
	}
	if got := dst.Package[0].Dependencies; len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("deps0 = %#v", got)
	}
	if dst.Package[1].Name != "b" || len(dst.Package[1].Dependencies) != 0 {
		t.Fatalf("pkg1 = %+v", dst.Package[1])
	}
}

func TestDirectBindStringSliceTypeMismatch(t *testing.T) {
	t.Parallel()
	type pkg struct {
		Dependencies []string
	}
	type root struct {
		Package []pkg `toml:"package"`
	}
	// Integer element cannot bind into []string; direct path must error, not corrupt.
	input := []byte(`
[[package]]
dependencies = ["ok", 1]
`)
	var dst root
	err := Unmarshal(input, &dst)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := errors.AsType[*TypeMismatchError](err); !ok {
		t.Fatalf("err = %T(%v)", err, err)
	}
}

func TestDirectBindUnknownTableKeysIgnored(t *testing.T) {
	t.Parallel()
	type known struct {
		Y int `toml:"y"`
	}
	type cfg struct {
		X     int   `toml:"x"`
		Known known `toml:"known"`
	}
	tests := map[string]struct {
		input string
	}{
		"unknown table": {
			input: "x = 1\n\n[unknown]\nx = 5\n\n[known]\ny = 2\n",
		},
		"unknown array table": {
			input: "x = 1\n\n[[unknown]]\nx = 5\n\n[known]\ny = 2\n",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var dst cfg
			if err := Unmarshal([]byte(tt.input), &dst); err != nil {
				t.Fatal(err)
			}
			if dst.X != 1 {
				t.Fatalf("x = %d, want 1 (keys under the unknown table must be ignored)", dst.X)
			}
			if dst.Known.Y != 2 {
				t.Fatalf("known.y = %d, want 2", dst.Known.Y)
			}
		})
	}
}

// fusedPkg and fusedDoc expose fields that the fused direct-decoding paths
// bind: scalar keys, a known []string, a [table] header and an [[array-table]]
// header. Keys that are not fields here exercise the fused skippers.
type fusedPkg struct {
	Name string   `toml:"name"`
	Deps []string `toml:"deps"`
}

type fusedDoc struct {
	A        int        `toml:"a"`
	B        int        `toml:"b"`
	LongName int        `toml:"longname"`
	Deps     []string   `toml:"deps"`
	Table    fusedPkg   `toml:"table"`
	Pkgs     []fusedPkg `toml:"pkgs"`
}

// decodeFusedBothPaths decodes input through the direct struct path and through
// map[string]any, which goes through Decoder.readToken for every statement and
// is the reference for the error each input must produce.
func decodeFusedBothPaths(t *testing.T, input string, opts ...Option) (doc fusedDoc, directErr, mapErr error) {
	t.Helper()
	o := UnmarshalOptions{DecoderOptions: opts}
	directErr = o.Unmarshal([]byte(input), &doc)
	var m map[string]any
	mapErr = o.Unmarshal([]byte(input), &m)
	return doc, directErr, mapErr
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// fusedErrorCase is one input for the fused-path error tables. An empty
// wantErr means the input is valid and must decode to want.
type fusedErrorCase struct {
	input   string
	opts    []Option
	wantErr string
	want    fusedDoc
}

func runFusedErrorCases(t *testing.T, tests map[string]fusedErrorCase) {
	t.Helper()
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, directErr, mapErr := decodeFusedBothPaths(t, tt.input, tt.opts...)
			if diff := gocmp.Diff(tt.wantErr, errString(directErr)); diff != "" {
				t.Errorf("direct struct path error mismatch for %q (-want +got):\n%s", tt.input, diff)
			}
			if diff := gocmp.Diff(tt.wantErr, errString(mapErr)); diff != "" {
				t.Errorf("map[string]any path error mismatch for %q (-want +got):\n%s", tt.input, diff)
			}
			if tt.wantErr == "" {
				if diff := gocmp.Diff(tt.want, got); diff != "" {
					t.Errorf("decoded value mismatch for %q (-want +got):\n%s", tt.input, diff)
				}
			}
		})
	}
}

func TestDirectBindFusedStatementLineEnd(t *testing.T) {
	t.Parallel()
	runFusedErrorCases(t, map[string]fusedErrorCase{
		"error: two assignments on one line": {
			input:   "a=1 b=2\n",
			wantErr: "toml: expected newline at line 1 col 5",
		},
		"error: table header followed by key on the same line": {
			input:   "[table] name=\"x\"\n",
			wantErr: "toml: expected newline at line 1 col 9",
		},
		"error: array-table header followed by key on the same line": {
			input:   "[[pkgs]] name=\"x\"\n",
			wantErr: "toml: expected newline at line 1 col 10",
		},
		"error: skipped unknown scalar followed by key": {
			input:   "zz=1 a=2\n",
			wantErr: "toml: expected newline at line 1 col 6",
		},
		"error: known string array followed by key": {
			input:   "deps=[\"x\"] a=1\n",
			wantErr: "toml: expected newline at line 1 col 12",
		},
		"error: skipped unknown string array followed by key": {
			input:   "zz=[\"x\"] a=1\n",
			wantErr: "toml: expected newline at line 1 col 10",
		},
		"error: second statement after header on a later line": {
			input:   "[table]\nname=\"x\" deps=[]\n",
			wantErr: "toml: expected newline at line 2 col 10",
		},
		"error: bare carriage return after value": {
			input:   "a=1\rb=2\n",
			wantErr: "toml: bare carriage return at line 1 col 4",
		},
		"error: control character in trailing comment": {
			input:   "a=1 # \x01\nb=2\n",
			wantErr: "toml: control character in comment at line 1 col 7",
		},
		"success: trailing comments after statements": {
			input: "a=1 # one\nb=2\t# two\n[table] # t\nname=\"x\" # n\n",
			want:  fusedDoc{A: 1, B: 2, Table: fusedPkg{Name: "x"}},
		},
		"success: CRLF line endings": {
			input: "a=1\r\nb=2\r\n[[pkgs]]\r\nname=\"p\"\r\n",
			want:  fusedDoc{A: 1, B: 2, Pkgs: []fusedPkg{{Name: "p"}}},
		},
		"success: trailing comment at end of input": {
			input: "a=1\nb=2 # last",
			want:  fusedDoc{A: 1, B: 2},
		},
	})
}

func TestDirectBindFusedStringArraySeparators(t *testing.T) {
	t.Parallel()
	runFusedErrorCases(t, map[string]fusedErrorCase{
		"error: known array missing comma": {
			input:   "deps=[\"x\" \"y\"]\n",
			wantErr: "toml: expected array separator at line 1 col 11",
		},
		"error: known array missing comma before non-string": {
			input:   "deps=[\"x\" 1]\n",
			wantErr: "toml: expected array separator at line 1 col 11",
		},
		"error: known array missing comma across lines": {
			input:   "deps=[\n  \"x\"\n  \"y\",\n]\n",
			wantErr: "toml: expected array separator at line 3 col 3",
		},
		"error: known array leading comma": {
			input:   "deps=[,\"x\"]\n",
			wantErr: "toml: unexpected comma at line 1 col 7",
		},
		"error: known array double comma": {
			input:   "deps=[\"x\",,\"y\"]\n",
			wantErr: "toml: unexpected comma at line 1 col 11",
		},
		"error: known array only comma": {
			input:   "deps=[,]\n",
			wantErr: "toml: unexpected comma at line 1 col 7",
		},
		"error: known array only two commas": {
			input:   "deps=[,,]\n",
			wantErr: "toml: unexpected comma at line 1 col 7",
		},
		"error: known array equals after element": {
			input:   "deps=[\"x\" = ]\n",
			wantErr: "toml: unexpected equals at line 1 col 11",
		},
		"error: known array closing brace after element": {
			input:   "deps=[\"x\" } ]\n",
			wantErr: "toml: expected value at line 1 col 11",
		},
		"error: known array unterminated at end of input": {
			input:   "deps=[\"x\",",
			wantErr: "toml: expected value at line 1 col 11",
		},
		"error: unknown array missing comma": {
			input:   "zz=[\"x\" \"y\"]\n",
			wantErr: "toml: expected array separator at line 1 col 9",
		},
		"error: unknown array leading comma": {
			input:   "zz=[,\"x\"]\n",
			wantErr: "toml: unexpected comma at line 1 col 5",
		},
		"error: unknown array double comma": {
			input:   "zz=[\"x\",,\"y\"]\n",
			wantErr: "toml: unexpected comma at line 1 col 9",
		},
		"error: unknown array only comma": {
			input:   "zz=[,]\n",
			wantErr: "toml: unexpected comma at line 1 col 5",
		},
		"error: array-table element array missing comma": {
			input:   "[[pkgs]]\ndeps=[\"x\" \"y\"]\n",
			wantErr: "toml: expected array separator at line 2 col 11",
		},
		"success: empty and trailing-comma arrays": {
			input: "deps=[\"x\",]\nzz=[]\nyy=[\"a\",\n]\n[table]\ndeps=[]\n",
			want:  fusedDoc{Deps: []string{"x"}, Table: fusedPkg{Deps: []string{}}},
		},
	})
}

func TestDirectBindFusedStringArrayComments(t *testing.T) {
	t.Parallel()
	runFusedErrorCases(t, map[string]fusedErrorCase{
		"error: known array control character in comment": {
			input:   "deps=[\"x\", # \x01\n\"y\"]\n",
			wantErr: "toml: control character in comment at line 1 col 14",
		},
		"error: known array bare carriage return ends comment": {
			input:   "deps=[\"x\", # c\r\"y\"]\n",
			wantErr: "toml: control character in comment at line 1 col 15",
		},
		"error: unknown array control character in comment": {
			input:   "zz=[\"x\", # \x7f\n\"y\"]\n",
			wantErr: "toml: control character in comment at line 1 col 12",
		},
		"success: comments inside arrays": {
			input: "deps=[ # open\r\n\"x\", # one\n\"y\" # two\n]\nzz=[\"a\", # c\n]\n",
			want:  fusedDoc{Deps: []string{"x", "y"}},
		},
	})
}

func TestDirectBindFusedLimits(t *testing.T) {
	t.Parallel()
	keyLimit := []Option{WithLimits(Limits{MaxKeyLength: 3})}
	stringLimit := []Option{WithLimits(Limits{MaxStringLength: 3})}
	runFusedErrorCases(t, map[string]fusedErrorCase{
		"error: unknown bare key longer than MaxKeyLength": {
			input:   "abcdefghij=1\n",
			opts:    keyLimit,
			wantErr: "toml: MaxKeyLength cap exceeded (limit=3, span=[0,10))",
		},
		"error: known bare key longer than MaxKeyLength": {
			input:   "longname=1\n",
			opts:    keyLimit,
			wantErr: "toml: MaxKeyLength cap exceeded (limit=3, span=[0,8))",
		},
		"error: table header longer than MaxKeyLength": {
			input:   "[table]\n",
			opts:    keyLimit,
			wantErr: "toml: MaxKeyLength cap exceeded (limit=3, span=[1,6))",
		},
		"error: table header padding counts toward MaxKeyLength": {
			input:   "[ table ]\n",
			opts:    []Option{WithLimits(Limits{MaxKeyLength: 5})},
			wantErr: "toml: MaxKeyLength cap exceeded (limit=5, span=[1,8))",
		},
		"error: array-table header longer than MaxKeyLength": {
			input:   "[[pkgs]]\n",
			opts:    keyLimit,
			wantErr: "toml: MaxKeyLength cap exceeded (limit=3, span=[2,6))",
		},
		"error: known string array element longer than MaxStringLength": {
			input:   "deps=[\"abcdefghij\"]\n",
			opts:    stringLimit,
			wantErr: "toml: MaxStringLength cap exceeded (limit=3, span=[6,18))",
		},
		"error: unknown string array element longer than MaxStringLength": {
			input:   "zz=['abcdefghij']\n",
			opts:    stringLimit,
			wantErr: "toml: MaxStringLength cap exceeded (limit=3, span=[4,16))",
		},
		"success: keys and strings within limits": {
			input: "a=1\ndeps=[\"x\"]\n",
			opts:  []Option{WithLimits(Limits{MaxKeyLength: 4, MaxStringLength: 3})},
			want:  fusedDoc{A: 1, Deps: []string{"x"}},
		},
	})
}
