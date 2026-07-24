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
