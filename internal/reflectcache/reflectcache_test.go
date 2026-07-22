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

package reflectcache

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLookupCachesMetadataAndRejectsTypedOmitempty(t *testing.T) {
	t.Parallel()

	type sample struct {
		Name string
		Zero string `toml:"zero,omitzero"`
		Skip string `toml:"-"`
		Bad  string `toml:"bad,omitempty"`
	}

	info, err := Lookup(reflect.TypeFor[sample]())
	if err == nil {
		t.Fatalf("Lookup() error = nil, want invalid tag option error")
	}
	var tagErr *InvalidTagOptionError
	if !errors.As(err, &tagErr) || tagErr.Option != "omitempty" {
		t.Fatalf("Lookup() error = %T(%v), want InvalidTagOptionError option=omitempty", err, err)
	}
	if info != nil {
		t.Fatalf("Lookup() info = %#v, want nil on error", info)
	}
}

func TestLookupCachesFieldMetadata(t *testing.T) {
	t.Parallel()

	type sample struct {
		Name string
		Zero string `toml:"zero,omitzero"`
		Skip string `toml:"-"`
	}

	info, err := Lookup(reflect.TypeFor[sample]())
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if info.Type != reflect.TypeFor[sample]() {
		t.Fatalf("Type = %v, want %v", info.Type, reflect.TypeFor[sample]())
	}
	if got, ok := info.ByName["Name"]; !ok || got.Name != "Name" || got.OmitZero {
		t.Fatalf("ByName[Name] = %#v, %v", got, ok)
	}
	if got, ok := info.ByName["name"]; !ok || got.Name != "Name" || got.OmitZero {
		t.Fatalf("ByName[name] = %#v, %v", got, ok)
	}
	if got, ok := info.ByName["zero"]; !ok || !got.OmitZero || got.Name != "zero" {
		t.Fatalf("ByName[zero] = %#v, %v", got, ok)
	}
	if _, ok := info.ByName["Skip"]; ok {
		t.Fatalf("Skip should not be indexed: %#v", info.ByName)
	}
	if len(info.Fields) != 2 {
		t.Fatalf("Fields len = %d, want 2", len(info.Fields))
	}
	if info.HasDuplicateNames {
		t.Fatal("HasDuplicateNames = true, want false")
	}
	if got, want := len(info.MarshalFields), 2; got != want {
		t.Fatalf("MarshalFields len = %d, want %d", got, want)
	}
	if got := []string{info.MarshalFields[0].Name, info.MarshalFields[1].Name}; got[0] != "Name" || got[1] != "zero" {
		t.Fatalf("MarshalFields order = %v, want [Name zero]", got)
	}
	if info.EncodeStruct == nil {
		t.Fatal("EncodeStruct = nil, want precomputed encoder")
	}

	again, err := Lookup(reflect.TypeFor[sample]())
	if err != nil {
		t.Fatalf("Lookup() cached error = %v", err)
	}
	if again != info {
		t.Fatalf("Lookup() cache miss: %p != %p", again, info)
	}
}

func TestLookupMarksDuplicateMarshalNames(t *testing.T) {
	t.Parallel()

	type sample struct {
		First  string `toml:"name"`
		Second string `toml:"name"`
	}

	info, err := Lookup(reflect.TypeFor[sample]())
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !info.HasDuplicateNames {
		t.Fatal("HasDuplicateNames = false, want true")
	}
	if got, want := len(info.MarshalFields), 2; got != want {
		t.Fatalf("MarshalFields len = %d, want %d", got, want)
	}
	if info.MarshalFields[0].Name != "name" || info.MarshalFields[1].Name != "name" {
		t.Fatalf("MarshalFields duplicate names = %#v", info.MarshalFields)
	}
	if info.EncodeStruct != nil {
		t.Fatalf("EncodeStruct = %p, want nil for duplicate names", info.EncodeStruct)
	}
}

func TestEncodeStruct_writesFastScalarsAndDelegatesFallbacks(t *testing.T) {
	type nested struct{ Value string }
	type sample struct {
		Name    string `toml:"name"`
		Empty   string `toml:"empty,omitzero"`
		Active  bool   `toml:"active"`
		Count   int64  `toml:"count"`
		ID      uint   `toml:"id"`
		Ratio   float64
		When    time.Time
		Child   nested `toml:"child"`
		Aliases []string
	}
	info, err := Lookup(reflect.TypeFor[sample]())
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if info.EncodeStruct == nil {
		t.Fatal("EncodeStruct = nil, want precomputed encoder")
	}

	oldFieldFallback := EncodeFieldFallback
	oldTableFallback := EncodeTableFallback
	t.Cleanup(func() {
		EncodeFieldFallback = oldFieldFallback
		EncodeTableFallback = oldTableFallback
	})
	var fallbackFields []string
	var fallbackTables []string
	EncodeFieldFallback = func(buf *bytes.Buffer, key string, v reflect.Value, _ []string) error {
		fallbackFields = append(fallbackFields, key)
		buf.WriteString(key)
		buf.WriteString(" = fallback\n")
		return nil
	}
	EncodeTableFallback = func(_ *bytes.Buffer, key string, _ reflect.Value, _ []string) error {
		fallbackTables = append(fallbackTables, key)
		return nil
	}

	var buf bytes.Buffer
	err = info.EncodeStruct(&buf, reflect.ValueOf(sample{
		Name:    "alpha",
		Active:  true,
		Count:   -42,
		ID:      9,
		Ratio:   1000,
		When:    time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC),
		Child:   nested{Value: "table"},
		Aliases: []string{"a", "b"},
	}), nil)
	if err != nil {
		t.Fatalf("EncodeStruct() error = %v", err)
	}

	got := buf.String()
	for _, want := range []string{
		"Ratio = 1000.0",
		"When = 2026-07-23T01:02:03Z",
		"active = true",
		"count = -42",
		"id = 9",
		"name = \"alpha\"",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("EncodeStruct() missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "empty") {
		t.Fatalf("EncodeStruct() included omitzero field\n%s", got)
	}
	if got, want := strings.Join(fallbackFields, ","), "Aliases"; got != want {
		t.Fatalf("field fallbacks = %q, want %q", got, want)
	}
	if got, want := strings.Join(fallbackTables, ","), "child"; got != want {
		t.Fatalf("table fallbacks = %q, want %q", got, want)
	}
}
