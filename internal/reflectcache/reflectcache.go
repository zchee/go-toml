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

// Package reflectcache caches TOML field metadata for the public facade.
package reflectcache

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// Field describes one exported struct field visible to the TOML facade.
type Field struct {
	Name     string
	Index    []int
	OmitZero bool
	Type     reflect.Type
}

// TypeInfo is cached metadata for a struct type.
type TypeInfo struct {
	Type          reflect.Type
	Fields        []Field
	MarshalFields []Field
	EncodeStruct  StructEncoder
	// LowerNames holds strings.ToLower(Fields[i].Name), parallel to Fields, so
	// the small-struct lookup fast path can match case-insensitive aliases
	// without hashing the map or allocating a lowercased key per decoded field.
	LowerNames        []string
	HasDuplicateNames bool
	ByName            map[string]Field
	// ByNameIndex maps the same keys as ByName to the index of the field within
	// Fields, letting hot-path lookups return a *Field without copying a Field.
	ByNameIndex map[string]int32
}

// InvalidTagOptionError reports unsupported TOML tag options.
type InvalidTagOptionError struct {
	Struct reflect.Type
	Field  string
	Option string
}

func (e *InvalidTagOptionError) Error() string {
	return "toml: unsupported struct tag option " + e.Option + " on " + e.Struct.String() + "." + e.Field
}

var cache sync.Map // map[reflect.Type]*TypeInfo

// Lookup returns cached metadata for t. t must be a struct type.
func Lookup(t reflect.Type) (*TypeInfo, error) {
	if t.Kind() != reflect.Struct {
		return nil, &InvalidTagOptionError{Struct: t, Option: "non-struct"}
	}
	if v, ok := cache.Load(t); ok {
		return v.(*TypeInfo), nil
	}
	info, err := build(t)
	if err != nil {
		return nil, err
	}
	if v, loaded := cache.LoadOrStore(t, info); loaded {
		return v.(*TypeInfo), nil
	}
	return info, nil
}

func build(t reflect.Type) (*TypeInfo, error) {
	info := &TypeInfo{
		Type:        t,
		ByName:      make(map[string]Field),
		ByNameIndex: make(map[string]int32),
	}
	seenNames := make(map[string]struct{})
	for sf := range t.Fields() {
		if sf.PkgPath != "" && !sf.Anonymous {
			continue
		}
		name, omit, skip, err := parseTag(t, &sf)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		idx := int32(len(info.Fields))
		field := Field{Name: name, Index: append([]int(nil), sf.Index...), OmitZero: omit, Type: sf.Type}
		if _, exists := info.ByName[name]; !exists {
			info.ByName[name] = field
			info.ByNameIndex[name] = idx
		}
		if _, exists := seenNames[name]; exists {
			info.HasDuplicateNames = true
		} else {
			seenNames[name] = struct{}{}
		}
		lower := strings.ToLower(name)
		if lower != name {
			if _, exists := info.ByName[lower]; !exists {
				info.ByName[lower] = field
				info.ByNameIndex[lower] = idx
			}
		}
		info.Fields = append(info.Fields, field)
		info.LowerNames = append(info.LowerNames, lower)
	}
	info.MarshalFields = slices.Clone(info.Fields)
	slices.SortFunc(info.MarshalFields, func(x, y Field) int {
		return cmp.Compare(x.Name, y.Name)
	})
	if !info.HasDuplicateNames {
		info.EncodeStruct = buildStructEncoder(info.MarshalFields)
	}
	return info, nil
}

func parseTag(t reflect.Type, sf *reflect.StructField) (name string, omitZero, skip bool, err error) {
	tag, ok := sf.Tag.Lookup("toml")
	if !ok {
		return "", false, false, nil
	}
	if tag == "-" {
		return "", false, true, nil
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, opt := range parts[1:] {
		switch opt {
		case "":
			continue
		case "omitzero":
			omitZero = true
		case "omitempty":
			return "", false, false, &InvalidTagOptionError{Struct: t, Field: sf.Name, Option: opt}
		default:
			return "", false, false, &InvalidTagOptionError{Struct: t, Field: sf.Name, Option: opt}
		}
	}
	return name, omitZero, false, nil
}
