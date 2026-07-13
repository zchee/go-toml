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

package testsuite

import (
	"math"
	"testing"

	"github.com/go-json-experiment/json"
)

func TestValueToTaggedJSONSpecialFloats(t *testing.T) {
	t.Parallel()

	body, err := ValueToTaggedJSON(map[string]any{
		"finite": 1.5,
		"nan":    math.NaN(),
		"posinf": math.Inf(1),
		"neginf": math.Inf(-1),
		"nested": map[string]any{
			"items": []any{math.NaN(), math.Inf(1)},
		},
	})
	if err != nil {
		t.Fatalf("ValueToTaggedJSON() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	nan := got["nan"].(map[string]any)
	if nan["value"] != "nan" {
		t.Fatalf("nan tag value = %#v, want %q", nan["value"], "nan")
	}
	posinf := got["posinf"].(map[string]any)
	if posinf["value"] != "inf" {
		t.Fatalf("posinf tag value = %#v, want %q", posinf["value"], "inf")
	}
	neginf := got["neginf"].(map[string]any)
	if neginf["value"] != "-inf" {
		t.Fatalf("neginf tag value = %#v, want %q", neginf["value"], "-inf")
	}
	nested := got["nested"].(map[string]any)
	items := nested["items"].([]any)
	if items[0].(map[string]any)["value"] != "nan" {
		t.Fatalf("nested.items[0] value = %#v, want %q", items[0], "nan")
	}
	if items[1].(map[string]any)["value"] != "inf" {
		t.Fatalf("nested.items[1] value = %#v, want %q", items[1], "inf")
	}
}
