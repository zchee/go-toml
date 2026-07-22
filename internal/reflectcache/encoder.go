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
	"encoding"
	"reflect"
	"strconv"
	"time"
)

// StructEncoder writes a struct value as TOML document entries.
type StructEncoder func(buf *bytes.Buffer, v reflect.Value, path []string) error

// EncodeFieldFallback writes one non-fast scalar field. It is assigned by the
// parent toml package at init time to avoid an import cycle.
var EncodeFieldFallback func(buf *bytes.Buffer, key string, v reflect.Value, path []string) error

// EncodeTableFallback writes one table-like field. It is assigned by the
// parent toml package at init time to avoid an import cycle.
var EncodeTableFallback func(buf *bytes.Buffer, key string, v reflect.Value, path []string) error

type fieldEncoder struct {
	name     string
	index    []int
	omitZero bool
	write    valueWriter
}

type tableValue struct {
	name  string
	value reflect.Value
}

type valueWriter func(buf *bytes.Buffer, key string, v reflect.Value, path []string) error

const quoteFallback = -2

func buildStructEncoder(fields []Field) StructEncoder {
	encoders := make([]fieldEncoder, 0, len(fields))
	for _, field := range fields {
		encoders = append(encoders, fieldEncoder{
			name:     field.Name,
			index:    append([]int(nil), field.Index...),
			omitZero: field.OmitZero,
			write:    writerForType(field.Type),
		})
	}
	return func(buf *bytes.Buffer, v reflect.Value, path []string) error {
		var tables []tableValue
		for _, field := range encoders {
			fv := v.FieldByIndex(field.index)
			if field.omitZero && fv.IsZero() {
				continue
			}
			if !fv.CanInterface() || isNilValue(fv) {
				continue
			}
			if isTableLike(fv) {
				tables = append(tables, tableValue{name: field.name, value: fv})
				continue
			}
			if err := field.write(buf, field.name, fv, path); err != nil {
				return err
			}
		}
		for _, table := range tables {
			if EncodeTableFallback == nil {
				return &MissingEncodeFallbackError{Kind: "table"}
			}
			if err := EncodeTableFallback(buf, table.name, table.value, path); err != nil {
				return err
			}
		}
		return nil
	}
}

// MissingEncodeFallbackError reports that the parent toml package did not wire
// the package-specific encoder fallback.
type MissingEncodeFallbackError struct {
	Kind string
}

func (e *MissingEncodeFallbackError) Error() string {
	return "toml: missing reflectcache encode fallback for " + e.Kind
}

func writerForType(t reflect.Type) valueWriter {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[time.Time]() {
		return writeTime
	}
	switch t.Kind() {
	case reflect.String:
		return writeString
	case reflect.Bool:
		return writeBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return writeInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return writeUint
	case reflect.Float32, reflect.Float64:
		return writeFloat
	default:
		return writeFallback
	}
}

func writeString(buf *bytes.Buffer, key string, v reflect.Value, _ []string) error {
	v = indirectValue(v)
	writeKeyPrefix(buf, key)
	writeQuotedString(buf, v.String())
	buf.WriteByte('\n')
	return nil
}

func writeBool(buf *bytes.Buffer, key string, v reflect.Value, _ []string) error {
	v = indirectValue(v)
	writeKeyPrefix(buf, key)
	b := buf.AvailableBuffer()
	b = strconv.AppendBool(b, v.Bool())
	b = append(b, '\n')
	buf.Write(b)
	return nil
}

func writeInt(buf *bytes.Buffer, key string, v reflect.Value, _ []string) error {
	v = indirectValue(v)
	writeKeyPrefix(buf, key)
	b := buf.AvailableBuffer()
	b = strconv.AppendInt(b, v.Int(), 10)
	b = append(b, '\n')
	buf.Write(b)
	return nil
}

func writeUint(buf *bytes.Buffer, key string, v reflect.Value, _ []string) error {
	v = indirectValue(v)
	writeKeyPrefix(buf, key)
	b := buf.AvailableBuffer()
	b = strconv.AppendUint(b, v.Uint(), 10)
	b = append(b, '\n')
	buf.Write(b)
	return nil
}

func writeFloat(buf *bytes.Buffer, key string, v reflect.Value, _ []string) error {
	v = indirectValue(v)
	writeKeyPrefix(buf, key)
	writeFloatValue(buf, v.Float(), v.Type().Bits())
	buf.WriteByte('\n')
	return nil
}

func writeTime(buf *bytes.Buffer, key string, v reflect.Value, _ []string) error {
	v = indirectValue(v)
	writeKeyPrefix(buf, key)
	b := buf.AvailableBuffer()
	b = v.Interface().(time.Time).AppendFormat(b, time.RFC3339Nano)
	b = append(b, '\n')
	buf.Write(b)
	return nil
}

func writeFallback(buf *bytes.Buffer, key string, v reflect.Value, path []string) error {
	if EncodeFieldFallback == nil {
		return &MissingEncodeFallbackError{Kind: "field"}
	}
	return EncodeFieldFallback(buf, key, v, path)
}

func isNilValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func isTableLike(v reflect.Value) bool {
	v = indirectValue(v)
	if !v.IsValid() || isScalarSpecialType(v.Type()) {
		return false
	}
	return isArrayOfTables(v) || v.Kind() == reflect.Struct || v.Kind() == reflect.Map
}

func isArrayOfTables(v reflect.Value) bool {
	v = indirectValue(v)
	if !v.IsValid() || (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) || v.Len() == 0 {
		return false
	}
	e := indirectValue(v.Index(0))
	return e.IsValid() && !isScalarSpecialType(e.Type()) && (e.Kind() == reflect.Struct || e.Kind() == reflect.Map)
}

func indirectValue(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func isScalarSpecialType(t reflect.Type) bool {
	return t == reflect.TypeFor[time.Time]() ||
		isTomlLocalDateType(t) ||
		(t.PkgPath() != "" && t.Implements(reflect.TypeFor[encoding.TextMarshaler]()))
}

func isTomlLocalDateType(t reflect.Type) bool {
	return t.PkgPath() == "github.com/zchee/go-toml" &&
		(t.Name() == "LocalDateTime" || t.Name() == "LocalDate" || t.Name() == "LocalTime")
}
