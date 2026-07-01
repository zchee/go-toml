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
	"strconv"
	"strings"
	"testing"
	"time"
)

// CmpJSON asserts that the decoded value have matches the expected toml-test
// value want for the given key, recursing through tables and arrays.
func CmpJSON(t *testing.T, key string, want, have any) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		cmpJSONMaps(t, key, w, have)
	case []any:
		cmpJSONArrays(t, key, w, have)
	default:
		t.Errorf(
			"Key '%s' in expected output should be a map or a list of maps, but it's a %T",
			key, want,
		)
	}
}

func cmpJSONMaps(t *testing.T, key string, want map[string]any, have any) {
	t.Helper()
	haveMap, ok := have.(map[string]any)
	if !ok {
		mismatch(t, key, "table", want, haveMap)
		return
	}

	// Check to make sure both or neither are values.
	if isValue(want) && !isValue(haveMap) {
		t.Fatalf("Key '%s' is supposed to be a value, but the parser reports it as a table", key)
	}
	if !isValue(want) && isValue(haveMap) {
		t.Fatalf("Key '%s' is supposed to be a table, but the parser reports it as a value", key)
	}
	if isValue(want) && isValue(haveMap) {
		cmpJSONValues(t, key, want, haveMap)
		return
	}

	// Check that the keys of each map are equivalent.
	for k := range want {
		if _, ok := haveMap[k]; !ok {
			bunk := kjoin(key, k)
			t.Fatalf("Could not find key '%s' in parser output.", bunk)
		}
	}
	for k := range haveMap {
		if _, ok := want[k]; !ok {
			bunk := kjoin(key, k)
			t.Fatalf("Could not find key '%s' in expected output.", bunk)
		}
	}

	// Okay, now make sure that each value is equivalent.
	for k := range want {
		CmpJSON(t, kjoin(key, k), want[k], haveMap[k])
	}
}

func cmpJSONArrays(t *testing.T, key string, want, have any) {
	t.Helper()
	wantSlice, ok := want.([]any)
	if !ok {
		panic(fmt.Sprintf("'value' should be a JSON array when 'type=array', but it is a %T", want))
	}

	haveSlice, ok := have.([]any)
	if !ok {
		t.Fatalf("Malformed output from your encoder: 'value' is not a JSON array: %T", have)
	}

	if len(wantSlice) != len(haveSlice) {
		t.Fatalf("Array lengths differ for key '%s':\n"+
			"  Expected:     %d\n"+
			"  Your encoder: %d",
			key, len(wantSlice), len(haveSlice))
	}
	for i := range wantSlice {
		CmpJSON(t, key, wantSlice[i], haveSlice[i])
	}
}

func cmpJSONValues(t *testing.T, key string, want, have map[string]any) {
	t.Helper()
	wantType, ok := want["type"].(string)
	if !ok {
		panic(fmt.Sprintf("'type' should be a string, but it is a %T", want["type"]))
	}

	haveType, ok := have["type"].(string)
	if !ok {
		t.Fatalf("Malformed output from your encoder: 'type' is not a string: %T", have["type"])
	}

	if wantType != haveType {
		valMismatch(t, key, wantType, haveType, want, have)
	}

	// If this is an array, then we've got to do some work to check equality.
	if wantType == "array" {
		cmpJSONArrays(t, key, want, have)
		return
	}

	// Atomic values are always strings
	wantVal, ok := want["value"].(string)
	if !ok {
		panic(fmt.Sprintf("'value' %v should be a string, but it is a %[1]T", want["value"]))
	}

	haveVal, ok := have["value"].(string)
	if !ok {
		panic(fmt.Sprintf("Malformed output from your encoder: %T is not a string", have["value"]))
	}

	// Excepting floats and datetimes, other values can be compared as strings.
	switch wantType {
	case "float":
		cmpFloats(t, key, wantVal, haveVal)
	case "datetime", "datetime-local", "date-local", "time-local":
		cmpAsDatetimes(t, key, wantType, wantVal, haveVal)
	default:
		cmpAsStrings(t, key, wantVal, haveVal)
	}
}

func cmpAsStrings(t *testing.T, key, want, have string) {
	t.Helper()
	if want != have {
		t.Fatalf("Values for key '%s' don't match:\n"+
			"  Expected:     %s\n"+
			"  Your encoder: %s",
			key, want, have)
	}
}

func cmpFloats(t *testing.T, key, want, have string) {
	t.Helper()
	// Special case for NaN, since NaN != NaN.
	if strings.HasSuffix(want, "nan") || strings.HasSuffix(have, "nan") {
		if want != have {
			t.Fatalf("Values for key '%s' don't match:\n"+
				"  Expected:     %v\n"+
				"  Your encoder: %v",
				key, want, have)
		}
		return
	}

	wantF, err := strconv.ParseFloat(want, 64)
	if err != nil {
		panic(fmt.Sprintf("Could not read '%s' as a float value for key '%s'", want, key))
	}

	haveF, err := strconv.ParseFloat(have, 64)
	if err != nil {
		panic(fmt.Sprintf("Malformed output from your encoder: key '%s' is not a float: '%s'", key, have))
	}

	if wantF != haveF {
		t.Fatalf("Values for key '%s' don't match:\n"+
			"  Expected:     %v\n"+
			"  Your encoder: %v",
			key, wantF, haveF)
	}
}

var datetimeRepl = strings.NewReplacer(
	" ", "T",
	"t", "T",
	"z", "Z",
)

var layouts = map[string]string{
	"datetime":       time.RFC3339Nano,
	"datetime-local": "2006-01-02T15:04:05.999999999",
	"date-local":     "2006-01-02",
	"time-local":     "15:04:05",
}

func cmpAsDatetimes(t *testing.T, key, kind, want, have string) {
	t.Helper()
	layout, ok := layouts[kind]
	if !ok {
		panic("should never happen")
	}

	wantT, err := time.Parse(layout, datetimeRepl.Replace(want))
	if err != nil {
		panic(fmt.Sprintf("Could not read '%s' as a datetime value for key '%s'", want, key))
	}

	haveT, err := time.Parse(layout, datetimeRepl.Replace(have))
	if err != nil {
		t.Fatalf("Malformed output from your encoder: key '%s' is not a datetime: '%s'", key, have)
		return
	}
	if !wantT.Equal(haveT) {
		t.Fatalf("Values for key '%s' don't match:\n"+
			"  Expected:     %v\n"+
			"  Your encoder: %v",
			key, wantT, haveT)
	}
}

func kjoin(old, key string) string {
	if old == "" {
		return key
	}
	return old + "." + key
}

func isValue(m map[string]any) bool {
	if len(m) != 2 {
		return false
	}
	if _, ok := m["type"]; !ok {
		return false
	}
	if _, ok := m["value"]; !ok {
		return false
	}
	return true
}

func mismatch(t *testing.T, key, wantType string, want, have any) {
	t.Helper()
	t.Fatalf("Key '%s' is not an %s but %[4]T:\n"+
		"  Expected:     %#[3]v\n"+
		"  Your encoder: %#[4]v",
		key, wantType, want, have)
}

func valMismatch(t *testing.T, key, wantType, haveType string, want, have any) {
	t.Helper()
	t.Fatalf("Key '%s' is not an %s but %s:\n"+
		"  Expected:     %#[3]v\n"+
		"  Your encoder: %#[4]v",
		key, wantType, haveType, want, have)
}
