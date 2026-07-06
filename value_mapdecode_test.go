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
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

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
