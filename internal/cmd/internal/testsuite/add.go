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
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/zchee/go-toml"
)

// addTag adds JSON tags to a data structure as expected by toml-test.
//
//nolint:cyclop // type-switch dispatch over every toml-test value kind; cohesive.
func addTag(tomlData any) (any, error) {
	// Switch on the data type.
	switch orig := tomlData.(type) {
	// A table: we don't need to add any tags, just recurse for every table
	// entry.
	case map[string]any:
		typed := make(map[string]any, len(orig))
		for k, v := range orig {
			tagged, err := addTag(v)
			if err != nil {
				return nil, err
			}
			typed[k] = tagged
		}
		return typed, nil

	// An array: we don't need to add any tags, just recurse for every table
	// entry.
	case []map[string]any:
		typed := make([]map[string]any, len(orig))
		for i, v := range orig {
			tagged, err := addTag(v)
			if err != nil {
				return nil, err
			}
			typed[i] = tagged.(map[string]any)
		}
		return typed, nil

	case []any:
		typed := make([]any, len(orig))
		for i, v := range orig {
			tagged, err := addTag(v)
			if err != nil {
				return nil, err
			}
			typed[i] = tagged
		}
		return typed, nil
	// Datetime: tag as datetime.
	case toml.LocalTime:
		return tag("time-local", canonicalizeLocalTimeText(orig.String())), nil

	case toml.LocalDate:
		return tag("date-local", orig.String()), nil

	case toml.LocalDateTime:
		return tag("datetime-local", canonicalizeLocalDateTimeText(orig.String())), nil

	case time.Time:
		return tag("datetime", orig.Format("2006-01-02T15:04:05.999999999Z07:00")), nil

	// Tag primitive values: bool, string, int, and float64.
	case bool:
		return tag("bool", strconv.FormatBool(orig)), nil

	case string:
		return tag("string", orig), nil

	case int64:
		return tag("integer", strconv.FormatInt(orig, 10)), nil

	case float64:
		switch {
		case math.IsNaN(orig):
			if math.Signbit(orig) {
				return tag("float", "-nan"), nil
			}
			return tag("float", "nan"), nil
		case math.IsInf(orig, 1):
			return tag("float", "inf"), nil
		case math.IsInf(orig, -1):
			return tag("float", "-inf"), nil
		default:
			return tag("float", fmt.Sprintf("%v", orig)), nil
		}

	default:
		return addTagReflect(reflect.ValueOf(tomlData))
	}
}

func addTagReflect(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil //nolint:nilnil // an invalid reflect value maps to a nil tagged value without error.
	}
	switch v.Kind() {
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unknown type: %T", v.Interface())
		}
		typed := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			tagged, err := addTag(iter.Value().Interface())
			if err != nil {
				return nil, err
			}
			typed[iter.Key().String()] = tagged
		}
		return typed, nil
	case reflect.Slice, reflect.Array:
		typed := make([]any, v.Len())
		for i := range typed {
			tagged, err := addTag(v.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			typed[i] = tagged
		}
		return typed, nil
	default:
		return nil, fmt.Errorf("unknown type: %T", v.Interface())
	}
}

func tag(typeName string, data any) map[string]any {
	return map[string]any{
		"type":  typeName,
		"value": data,
	}
}

func canonicalizeLocalTimeText(text string) string {
	if strings.Count(text, ":") == 1 {
		return text + ":00"
	}
	return text
}

func canonicalizeLocalDateTimeText(text string) string {
	if idx := strings.LastIndexAny(text, "T "); idx >= 0 {
		tail := text[idx+1:]
		if strings.Count(tail, ":") == 1 {
			return text + ":00"
		}
	}
	return text
}
