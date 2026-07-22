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
	"github.com/go-json-experiment/json/jsontext"
)

type testMap map[string]any

func TestValueToTaggedJSONNestedPublicContainers(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"title":  "demo",
		"count":  int64(3),
		"active": true,
		"nested": map[string]any{
			"name": "inner",
			"items": []any{
				map[string]any{"x": int64(1), "y": int64(2)},
				"plain",
			},
		},
	}

	tagged, err := addTag(input)
	if err != nil {
		t.Fatalf("addTag() error = %v", err)
	}
	got, err := json.Marshal(tagged, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		t.Fatalf("json.MarshalIndent(addTag()) error = %v", err)
	}
	const want = `{
  "active": {
    "type": "bool",
    "value": "true"
  },
  "count": {
    "type": "integer",
    "value": "3"
  },
  "nested": {
    "items": [
      {
        "x": {
          "type": "integer",
          "value": "1"
        },
        "y": {
          "type": "integer",
          "value": "2"
        }
      },
      {
        "type": "string",
        "value": "plain"
      }
    ],
    "name": {
      "type": "string",
      "value": "inner"
    }
  },
  "title": {
    "type": "string",
    "value": "demo"
  }
}`
	if got := string(got); got != want {
		t.Fatalf("addTag() output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestValueToTaggedJSONEncodesNestedTableContainers(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"title": "demo",
		"tags": []any{
			map[string]any{"name": "one"},
			map[string]any{"name": "two"},
		},
	}

	got, err := ValueToTaggedJSON(input)
	if err != nil {
		t.Fatalf("ValueToTaggedJSON() error = %v", err)
	}
	const want = `{
  "tags": [
    {
      "name": {
        "type": "string",
        "value": "one"
      }
    },
    {
      "name": {
        "type": "string",
        "value": "two"
      }
    }
  ],
  "title": {
    "type": "string",
    "value": "demo"
  }
}`
	if got := string(got); got != want {
		t.Fatalf("ValueToTaggedJSON() output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestValueToTaggedJSONEncodesNestedContainers(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"title": "demo",
		"table": map[string]any{
			"count": int64(3),
			"items": []any{
				map[string]any{"name": "one"},
			},
		},
	}

	got, err := ValueToTaggedJSON(input)
	if err != nil {
		t.Fatalf("ValueToTaggedJSON() error = %v", err)
	}
	const want = `{
  "table": {
    "count": {
      "type": "integer",
      "value": "3"
    },
    "items": [
      {
        "name": {
          "type": "string",
          "value": "one"
        }
      }
    ]
  },
  "title": {
    "type": "string",
    "value": "demo"
  }
}`
	if got := string(got); got != want {
		t.Fatalf("ValueToTaggedJSON() output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestAddTagPreservesSignedNaN(t *testing.T) {
	t.Parallel()

	neg := math.Float64frombits(0xfff8000000000000)
	tagged, err := addTag(map[string]any{"value": neg})
	if err != nil {
		t.Fatalf("addTag() error = %v", err)
	}
	got, err := json.Marshal(tagged, json.Deterministic(true))
	if err != nil {
		t.Fatalf("json.Marshal(addTag()) error = %v", err)
	}
	if string(got) != `{"value":{"type":"float","value":"-nan"}}` {
		t.Fatalf("addTag() signed NaN mismatch: got %s", got)
	}
}

func TestAddTagSupportsNamedMapTypes(t *testing.T) {
	t.Parallel()

	doc := testMap{
		"title": "demo",
		"server": testMap{
			"enabled": true,
			"meta": testMap{
				"name": "api",
			},
		},
		"items": []testMap{
			{"name": "one"},
		},
	}

	got, err := addTag(doc)
	if err != nil {
		t.Fatalf("addTag(testMap) error = %v", err)
	}

	root, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("addTag(testMap) = %T(%#v), want map[string]any", got, got)
	}
	server, ok := root["server"].(map[string]any)
	if !ok {
		t.Fatalf("server = %T(%#v), want map[string]any", root["server"], root["server"])
	}
	if got, want := server["enabled"].(map[string]any)["type"], "bool"; got != want {
		t.Fatalf("server.enabled.type = %v, want %v", got, want)
	}
	meta, ok := server["meta"].(map[string]any)
	if !ok {
		t.Fatalf("server.meta = %T(%#v), want map[string]any", server["meta"], server["meta"])
	}
	if got, want := meta["name"].(map[string]any)["type"], "string"; got != want {
		t.Fatalf("server.meta.name.type = %v, want %v", got, want)
	}
	items, ok := root["items"].([]any)
	if !ok {
		t.Fatalf("items = %T(%#v), want []any", root["items"], root["items"])
	}
	item0, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("items[0] = %T(%#v), want map[string]any", items[0], items[0])
	}
	if got, want := item0["name"].(map[string]any)["type"], "string"; got != want {
		t.Fatalf("items[0].name.type = %v, want %v", got, want)
	}
}
