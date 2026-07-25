package toml

import (
	"errors"
	"testing"
)

func TestFusedBindSimpleAssignment(t *testing.T) {
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

func TestFusedBindSkipsUnknownStringArray(t *testing.T) {
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

func TestFusedBindStringSliceArray(t *testing.T) {
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

func TestFusedBindFallbackDoesNotCorruptState(t *testing.T) {
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

func TestFusedBindArrayTableHeader(t *testing.T) {
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

func TestFusedBindMismatchPath(t *testing.T) {
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

func TestFusedBindStringSliceEmptyAndTrailingComma(t *testing.T) {
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

func TestFusedBindStringSliceWithComments(t *testing.T) {
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

func TestFusedBindStringSliceComplexFallback(t *testing.T) {
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

func TestFusedBindStringSliceMixedElementFallback(t *testing.T) {
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

func TestFusedBindQuotedKeyFallback(t *testing.T) {
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

func TestFusedBindUnknownArrayThenKnownFields(t *testing.T) {
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

func TestFusedBindKnownDepsThenNextPackage(t *testing.T) {
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

func TestFusedBindStringSliceTypeMismatch(t *testing.T) {
	t.Parallel()
	type pkg struct {
		Dependencies []string
	}
	type root struct {
		Package []pkg `toml:"package"`
	}
	// Integer element cannot bind into []string; fused path must error, not corrupt.
	input := []byte(`
[[package]]
dependencies = ["ok", 1]
`)
	var dst root
	err := Unmarshal(input, &dst)
	if err == nil {
		t.Fatal("expected error")
	}
	var mismatch *TypeMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %T(%v)", err, err)
	}
}

func TestFusedBindUnknownTableKeysIgnored(t *testing.T) {
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
