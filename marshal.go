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
	"bytes"
	"cmp"
	"encoding"
	"errors"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zchee/go-toml/internal/reflectcache"
)

var (
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	timeType          = reflect.TypeFor[time.Time]()
	localDateTimeType = reflect.TypeFor[LocalDateTime]()
	localDateType     = reflect.TypeFor[LocalDate]()
	localTimeType     = reflect.TypeFor[LocalTime]()
)

const (
	maxPooledStringKeys = 1024
	maxMarshalSizeHint  = 4 << 20

	quoteFallback = -2
)

var stringKeysPool sync.Pool

type marshalEntry struct {
	name  string
	value reflect.Value
}

// Marshal encodes v as a TOML document.
func Marshal(v any) ([]byte, error) {
	return marshalWithOptions(v, MarshalOptions{})
}

func marshalWithOptions(v any, opts MarshalOptions) ([]byte, error) {
	var buf bytes.Buffer
	if hint := marshalSizeHint(v); hint > 0 {
		buf.Grow(hint)
	}
	if err := marshalToBuffer(&buf, v, opts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func marshalToBuffer(buf *bytes.Buffer, v any, opts MarshalOptions) error {
	if m, ok := v.(MarshalerTo); ok {
		return m.MarshalTOMLTo(NewEncoder(buf, opts))
	}
	if m, ok := v.(map[string]any); ok {
		return encodeAnyMapDocument(buf, m, nil)
	}
	return encodeDocument(buf, reflect.ValueOf(v), nil)
}

func encodeDocument(buf *bytes.Buffer, v reflect.Value, path []string) error {
	v = indirectValue(v)
	if !v.IsValid() {
		return &UnsupportedTypeError{Type: "nil"}
	}
	switch v.Kind() {
	case reflect.Map:
		return encodeMapDocument(buf, v, path)
	case reflect.Struct:
		return encodeStructDocument(buf, v, path)
	default:
		return &UnsupportedTypeError{Type: v.Type().String()}
	}
}

func encodeStructDocument(buf *bytes.Buffer, v reflect.Value, path []string) error {
	info, err := reflectcache.Lookup(v.Type())
	if err != nil {
		return normalizeReflectcacheError(err)
	}
	if info.HasDuplicateNames {
		entries, err := structMarshalEntries(v)
		if err != nil {
			return err
		}
		return encodeEntriesDocument(buf, entries, path)
	}
	var tables []marshalEntry
	for _, field := range info.MarshalFields {
		value, ok := marshalFieldValue(v, field)
		if !ok {
			continue
		}
		if isTableLike(value) {
			tables = append(tables, marshalEntry{name: field.Name, value: value})
			continue
		}
		if err := writeKeyValue(buf, field.Name, value); err != nil {
			return err
		}
	}
	for _, entry := range tables {
		if isArrayOfTables(entry.value) {
			items := indirectValue(entry.value)
			nextPath := appendPath(path, entry.name)
			for i := range items.Len() {
				buf.WriteByte('\n')
				if err := writeHeader(buf, nextPath, true); err != nil {
					return err
				}
				if err := encodeDocument(buf, items.Index(i), nextPath); err != nil {
					return err
				}
			}
			continue
		}
		buf.WriteByte('\n')
		nextPath := appendPath(path, entry.name)
		if err := writeHeader(buf, nextPath, false); err != nil {
			return err
		}
		if err := encodeDocument(buf, entry.value, nextPath); err != nil {
			return err
		}
	}
	return nil
}

func encodeEntriesDocument(buf *bytes.Buffer, entries []marshalEntry, path []string) error {
	var tables []marshalEntry
	for _, entry := range entries {
		if isTableLike(entry.value) {
			tables = append(tables, entry)
			continue
		}
		if err := writeKeyValue(buf, entry.name, entry.value); err != nil {
			return err
		}
	}
	for _, entry := range tables {
		if isArrayOfTables(entry.value) {
			items := indirectValue(entry.value)
			nextPath := appendPath(path, entry.name)
			for i := range items.Len() {
				buf.WriteByte('\n')
				if err := writeHeader(buf, nextPath, true); err != nil {
					return err
				}
				if err := encodeDocument(buf, items.Index(i), nextPath); err != nil {
					return err
				}
			}
			continue
		}
		buf.WriteByte('\n')
		nextPath := appendPath(path, entry.name)
		if err := writeHeader(buf, nextPath, false); err != nil {
			return err
		}
		if err := encodeDocument(buf, entry.value, nextPath); err != nil {
			return err
		}
	}
	return nil
}

func encodeMapDocument(buf *bytes.Buffer, v reflect.Value, path []string) error {
	entries, err := sortedMapEntries(v)
	if err != nil {
		return err
	}
	var tables []marshalEntry
	for _, entry := range entries {
		if isTableLike(entry.value) {
			tables = append(tables, entry)
			continue
		}
		if err := writeKeyValue(buf, entry.name, entry.value); err != nil {
			return err
		}
	}
	for _, entry := range tables {
		if isArrayOfTables(entry.value) {
			items := indirectValue(entry.value)
			nextPath := appendPath(path, entry.name)
			for i := range items.Len() {
				buf.WriteByte('\n')
				if err := writeHeader(buf, nextPath, true); err != nil {
					return err
				}
				if err := encodeDocument(buf, items.Index(i), nextPath); err != nil {
					return err
				}
			}
			continue
		}
		buf.WriteByte('\n')
		nextPath := appendPath(path, entry.name)
		if err := writeHeader(buf, nextPath, false); err != nil {
			return err
		}
		if err := encodeDocument(buf, entry.value, nextPath); err != nil {
			return err
		}
	}
	return nil
}

func encodeAnyMapDocument(buf *bytes.Buffer, m map[string]any, path []string) error {
	keys := sortedStringKeys(m)
	defer recycleStringKeys(keys)
	for _, key := range keys {
		value := m[key]
		if isTableLikeAny(value) {
			continue
		}
		if err := writeKeyValueAny(buf, key, value); err != nil {
			return err
		}
	}
	for _, key := range keys {
		value := m[key]
		if !isTableLikeAny(value) {
			continue
		}
		if isArrayOfTablesAny(value) {
			items := value.([]any)
			nextPath := appendPath(path, key)
			for i := range items {
				buf.WriteByte('\n')
				if err := writeHeader(buf, nextPath, true); err != nil {
					return err
				}
				if err := encodeAnyDocument(buf, items[i], nextPath); err != nil {
					return err
				}
			}
			continue
		}
		buf.WriteByte('\n')
		nextPath := appendPath(path, key)
		if err := writeHeader(buf, nextPath, false); err != nil {
			return err
		}
		if err := encodeAnyDocument(buf, value, nextPath); err != nil {
			return err
		}
	}
	return nil
}

func encodeAnyDocument(buf *bytes.Buffer, value any, path []string) error {
	switch x := value.(type) {
	case map[string]any:
		return encodeAnyMapDocument(buf, x, path)
	default:
		return encodeDocument(buf, reflect.ValueOf(value), path)
	}
}

func writeKey(buf *bytes.Buffer, key string) error {
	k, err := formatKey(key)
	if err != nil {
		return err
	}
	buf.WriteString(k)
	return nil
}

func writeKeyValue(buf *bytes.Buffer, key string, v reflect.Value) error {
	if err := writeKey(buf, key); err != nil {
		return err
	}
	buf.WriteString(" = ")
	if err := writeValue(buf, v); err != nil {
		return err
	}
	buf.WriteByte('\n')
	return nil
}

func writeKeyValueAny(buf *bytes.Buffer, key string, value any) error {
	if err := writeKey(buf, key); err != nil {
		return err
	}
	buf.WriteString(" = ")
	if err := writeAnyValue(buf, value); err != nil {
		return err
	}
	buf.WriteByte('\n')
	return nil
}

func writeHeader(buf *bytes.Buffer, path []string, array bool) error {
	if array {
		buf.WriteString("[[")
	} else {
		buf.WriteByte('[')
	}
	for i, p := range path {
		if i > 0 {
			buf.WriteByte('.')
		}
		if err := writeKey(buf, p); err != nil {
			return err
		}
	}
	if array {
		buf.WriteString("]]\n")
	} else {
		buf.WriteString("]\n")
	}
	return nil
}

//nolint:cyclop // dispatch over all reflect kinds for TOML encoding; cohesive.
func writeValue(buf *bytes.Buffer, v reflect.Value) error {
	v = indirectValue(v)
	if !v.IsValid() {
		return &UnsupportedTypeError{Type: "nil"}
	}
	if ok, err := writeSpecialValue(buf, v); ok || err != nil {
		return err
	}
	switch v.Kind() {
	case reflect.String:
		return writeQuotedString(buf, v.String())
	case reflect.Bool:
		b := buf.AvailableBuffer()
		b = strconv.AppendBool(b, v.Bool())
		buf.Write(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b := buf.AvailableBuffer()
		b = strconv.AppendInt(b, v.Int(), 10)
		buf.Write(b)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		b := buf.AvailableBuffer()
		b = strconv.AppendUint(b, v.Uint(), 10)
		buf.Write(b)
		return nil
	case reflect.Float32, reflect.Float64:
		writeFloat(buf, v.Float(), v.Type().Bits())
		return nil
	case reflect.Slice, reflect.Array:
		buf.WriteByte('[')
		for i := range v.Len() {
			if i > 0 {
				buf.WriteString(", ")
			}
			if err := writeValue(buf, v.Index(i)); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case reflect.Map, reflect.Struct:
		return writeInlineTable(buf, v)
	case reflect.Interface:
		if v.IsNil() {
			return &UnsupportedTypeError{Type: "nil interface"}
		}
		return writeValue(buf, v.Elem())
	default:
		return &UnsupportedTypeError{Type: v.Type().String()}
	}
}

//nolint:cyclop,funlen // type-switch dispatch over all encodable Go value types; cohesive.
func writeAnyValue(buf *bytes.Buffer, value any) error {
	switch x := value.(type) {
	case nil:
		return &UnsupportedTypeError{Type: "nil"}
	case string:
		return writeQuotedString(buf, x)
	case bool:
		b := buf.AvailableBuffer()
		b = strconv.AppendBool(b, x)
		buf.Write(b)
		return nil
	case int:
		b := buf.AvailableBuffer()
		b = strconv.AppendInt(b, int64(x), 10)
		buf.Write(b)
		return nil
	case int8:
		b := buf.AvailableBuffer()
		b = strconv.AppendInt(b, int64(x), 10)
		buf.Write(b)
		return nil
	case int16:
		b := buf.AvailableBuffer()
		b = strconv.AppendInt(b, int64(x), 10)
		buf.Write(b)
		return nil
	case int32:
		b := buf.AvailableBuffer()
		b = strconv.AppendInt(b, int64(x), 10)
		buf.Write(b)
		return nil
	case int64:
		b := buf.AvailableBuffer()
		b = strconv.AppendInt(b, x, 10)
		buf.Write(b)
		return nil
	case uint:
		b := buf.AvailableBuffer()
		b = strconv.AppendUint(b, uint64(x), 10)
		buf.Write(b)
		return nil
	case uint8:
		b := buf.AvailableBuffer()
		b = strconv.AppendUint(b, uint64(x), 10)
		buf.Write(b)
		return nil
	case uint16:
		b := buf.AvailableBuffer()
		b = strconv.AppendUint(b, uint64(x), 10)
		buf.Write(b)
		return nil
	case uint32:
		b := buf.AvailableBuffer()
		b = strconv.AppendUint(b, uint64(x), 10)
		buf.Write(b)
		return nil
	case uint64:
		b := buf.AvailableBuffer()
		b = strconv.AppendUint(b, x, 10)
		buf.Write(b)
		return nil
	case uintptr:
		b := buf.AvailableBuffer()
		b = strconv.AppendUint(b, uint64(x), 10)
		buf.Write(b)
		return nil
	case float32:
		writeFloat(buf, float64(x), 32)
		return nil
	case float64:
		writeFloat(buf, x, 64)
		return nil
	case time.Time:
		b := buf.AvailableBuffer()
		b = x.AppendFormat(b, time.RFC3339Nano)
		buf.Write(b)
		return nil
	case LocalDateTime:
		if err := validateDate(x.Year, x.Month, x.Day); err != nil {
			return nil
		}
		if err := validateLocalClock(x.Hour, x.Minute, x.Second, x.Nanosecond, x.nanoDigits); err != nil {
			return nil
		}
		b := buf.AvailableBuffer()
		b = appendDate(b, x.Year, x.Month, x.Day)
		b = append(b, 'T')
		b = appendTime(b, x.Hour, x.Minute, x.Second, x.Nanosecond, x.nanoDigits, x.hasSecond)
		buf.Write(b)
		return nil
	case LocalDate:
		if err := validateDate(x.Year, x.Month, x.Day); err != nil {
			return nil
		}
		b := buf.AvailableBuffer()
		b = appendDate(b, x.Year, x.Month, x.Day)
		buf.Write(b)
		return nil
	case LocalTime:
		if err := validateLocalClock(x.Hour, x.Minute, x.Second, x.Nanosecond, x.nanoDigits); err != nil {
			return nil
		}
		b := buf.AvailableBuffer()
		b = appendTime(b, x.Hour, x.Minute, x.Second, x.Nanosecond, x.nanoDigits, x.hasSecond)
		buf.Write(b)
		return nil
	case encoding.TextMarshaler:
		text, err := x.MarshalText()
		if err != nil {
			return err
		}
		return writeQuotedString(buf, string(text))
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteString(", ")
			}
			if err := writeAnyValue(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case map[string]any:
		return writeInlineAnyMap(buf, x)
	default:
		return writeValue(buf, reflect.ValueOf(value))
	}
}

func writeSpecialValue(buf *bytes.Buffer, v reflect.Value) (bool, error) {
	if !v.CanInterface() {
		return false, nil
	}
	switch lookupSpecialKind(v.Type()) {
	case specialKindTime:
		t := v.Interface().(time.Time)
		b := buf.AvailableBuffer()
		b = t.AppendFormat(b, time.RFC3339Nano)
		buf.Write(b)
		return true, nil
	case specialKindLocalDateTime:
		dt := v.Interface().(LocalDateTime)
		if err := validateDate(dt.Year, dt.Month, dt.Day); err != nil {
			return true, nil
		}
		if err := validateLocalClock(dt.Hour, dt.Minute, dt.Second, dt.Nanosecond, dt.nanoDigits); err != nil {
			return true, nil
		}
		b := buf.AvailableBuffer()
		b = appendDate(b, dt.Year, dt.Month, dt.Day)
		b = append(b, 'T')
		b = appendTime(b, dt.Hour, dt.Minute, dt.Second, dt.Nanosecond, dt.nanoDigits, dt.hasSecond)
		buf.Write(b)
		return true, nil
	case specialKindLocalDate:
		d := v.Interface().(LocalDate)
		if err := validateDate(d.Year, d.Month, d.Day); err != nil {
			return true, nil
		}
		b := buf.AvailableBuffer()
		b = appendDate(b, d.Year, d.Month, d.Day)
		buf.Write(b)
		return true, nil
	case specialKindLocalTime:
		lt := v.Interface().(LocalTime)
		if err := validateLocalClock(lt.Hour, lt.Minute, lt.Second, lt.Nanosecond, lt.nanoDigits); err != nil {
			return true, nil
		}
		b := buf.AvailableBuffer()
		b = appendTime(b, lt.Hour, lt.Minute, lt.Second, lt.Nanosecond, lt.nanoDigits, lt.hasSecond)
		buf.Write(b)
		return true, nil
	case specialKindTextMarshaler:
		text, err := v.Interface().(encoding.TextMarshaler).MarshalText()
		if err != nil {
			return true, err
		}
		return true, writeQuotedString(buf, string(text))
	default:
		return false, nil
	}
}

// errInvalidUTF8 reports a Go string that has no TOML representation:
// TOML strings hold Unicode code points, so malformed UTF-8 cannot be
// carried raw or through any escape sequence.
var errInvalidUTF8 = errors.New("toml: cannot encode string containing invalid UTF-8")

func writeQuotedString(buf *bytes.Buffer, s string) error {
	switch first := asciiQuoteEscapeIndex(s); first {
	case -1:
		buf.WriteByte('"')
		buf.WriteString(s)
		buf.WriteByte('"')
		return nil
	case quoteFallback:
		b, err := appendBasicString(buf.AvailableBuffer(), s)
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil
	default:
		writeQuotedASCIIString(buf, s, first)
		return nil
	}
}

func asciiQuoteEscapeIndex(s string) int {
	first := -1
	for i := range len(s) {
		c := s[i]
		switch {
		case c < 0x20 || c >= 0x7f:
			return quoteFallback
		case c == '"' || c == '\\':
			if first < 0 {
				first = i
			}
		}
	}
	return first
}

// appendBasicString appends s quoted as a single-line TOML basic string.
// Only escape forms shared by TOML 1.0 and 1.1 are emitted: the named
// escapes for \b, \t, \n, \f, \r, quotation mark, and backslash, and
// \u00XX for the remaining control characters (U+0000..U+001F, U+007F,
// and the C1 range U+0080..U+009F, which is escaped for robustness even
// though the spec permits it raw).
func appendBasicString(b []byte, s string) ([]byte, error) {
	const hexDigits = "0123456789ABCDEF"
	b = append(b, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x20 && c < 0x7f {
			if c == '"' || c == '\\' {
				b = append(b, s[start:i]...)
				b = append(b, '\\', c)
				i++
				start = i
				continue
			}
			i++
			continue
		}
		if c < utf8.RuneSelf {
			b = append(b, s[start:i]...)
			switch c {
			case '\b':
				b = append(b, '\\', 'b')
			case '\t':
				b = append(b, '\\', 't')
			case '\n':
				b = append(b, '\\', 'n')
			case '\f':
				b = append(b, '\\', 'f')
			case '\r':
				b = append(b, '\\', 'r')
			default:
				b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0x0f])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, errInvalidUTF8
		}
		if r <= 0x9f {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', '0', '0', hexDigits[byte(r)>>4], hexDigits[byte(r)&0x0f])
			i += size
			start = i
			continue
		}
		i += size
	}
	b = append(b, s[start:]...)
	b = append(b, '"')
	return b, nil
}

func writeQuotedASCIIString(buf *bytes.Buffer, s string, first int) {
	b := buf.AvailableBuffer()
	b = append(b, '"')
	start := 0
	for i := first; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			b = append(b, s[start:i]...)
			b = append(b, '\\', s[i])
			start = i + 1
		}
	}
	b = append(b, s[start:]...)
	b = append(b, '"')
	buf.Write(b)
}

func writeFloat(buf *bytes.Buffer, value float64, bitSize int) {
	switch {
	case math.IsInf(value, 1):
		buf.WriteString("inf")
	case math.IsInf(value, -1):
		buf.WriteString("-inf")
	case math.IsNaN(value):
		if math.Signbit(value) {
			buf.WriteString("-nan")
		} else {
			buf.WriteString("nan")
		}
	default:
		b := buf.AvailableBuffer()
		b = strconv.AppendFloat(b, value, 'g', -1, bitSize)
		if !bytes.ContainsAny(b, ".eE") {
			b = append(b, '.', '0')
		}
		buf.Write(b)
	}
}

func writeInlineTable(buf *bytes.Buffer, v reflect.Value) error {
	v = indirectValue(v)
	if !v.IsValid() {
		return &UnsupportedTypeError{Type: "nil"}
	}
	buf.WriteString("{ ")
	switch v.Kind() {
	case reflect.Map:
		entries, err := sortedMapEntries(v)
		if err != nil {
			return err
		}
		for i, entry := range entries {
			if i > 0 {
				buf.WriteString(", ")
			}
			if err := writeKey(buf, entry.name); err != nil {
				return err
			}
			buf.WriteString(" = ")
			if err := writeValue(buf, entry.value); err != nil {
				return err
			}
		}
	case reflect.Struct:
		if err := writeInlineStructTable(buf, v); err != nil {
			return err
		}
	default:
		return &UnsupportedTypeError{Type: v.Type().String()}
	}
	buf.WriteString(" }")
	return nil
}

func writeInlineAnyMap(buf *bytes.Buffer, m map[string]any) error {
	buf.WriteString("{ ")
	keys := sortedStringKeys(m)
	defer recycleStringKeys(keys)
	for i, key := range keys {
		if i > 0 {
			buf.WriteString(", ")
		}
		if err := writeKey(buf, key); err != nil {
			return err
		}
		buf.WriteString(" = ")
		if err := writeAnyValue(buf, m[key]); err != nil {
			return err
		}
	}
	buf.WriteString(" }")
	return nil
}

func writeInlineStructTable(buf *bytes.Buffer, v reflect.Value) error {
	info, err := reflectcache.Lookup(v.Type())
	if err != nil {
		return normalizeReflectcacheError(err)
	}
	if info.HasDuplicateNames {
		entries, err := structMarshalEntries(v)
		if err != nil {
			return err
		}
		for i, entry := range entries {
			if i > 0 {
				buf.WriteString(", ")
			}
			if err := writeKey(buf, entry.name); err != nil {
				return err
			}
			buf.WriteString(" = ")
			if err := writeValue(buf, entry.value); err != nil {
				return err
			}
		}
		return nil
	}
	first := true
	for _, field := range info.MarshalFields {
		value, ok := marshalFieldValue(v, field)
		if !ok {
			continue
		}
		if !first {
			buf.WriteString(", ")
		}
		first = false
		if err := writeKey(buf, field.Name); err != nil {
			return err
		}
		buf.WriteString(" = ")
		if err := writeValue(buf, value); err != nil {
			return err
		}
	}
	return nil
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

func isNilValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func marshalSizeHint(v any) int {
	if v == nil {
		return 0
	}
	hint := estimateAnyMarshalSize(v, 0)
	if hint <= 0 {
		return 0
	}
	return min(hint, maxMarshalSizeHint)
}

func estimateAnyMarshalSize(v any, depth int) int {
	switch x := v.(type) {
	case nil:
		return 0
	case string:
		return len(x) + 2
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr, float32, float64:
		return 24
	case time.Time, LocalDateTime, LocalDate, LocalTime, encoding.TextMarshaler:
		return 40
	case []any:
		return estimateAnySliceMarshalSize(x, depth+1)
	case map[string]any:
		return estimateAnyMapMarshalSize(x, depth+1)
	default:
		return estimateReflectMarshalSize(reflect.ValueOf(v), depth+1)
	}
}

func estimateAnySliceMarshalSize(items []any, depth int) int {
	if len(items) == 0 {
		return 2
	}
	limit := min(len(items), 8)
	sum := 0
	for _, item := range items[:limit] {
		sum += estimateAnyMarshalSize(item, depth+1) + 2
	}
	return 2 + len(items)*max(1, sum/limit)
}

func estimateAnyMapMarshalSize(m map[string]any, depth int) int {
	if depth > 8 {
		return len(m) * 64
	}
	size := len(m) * 6
	for key, value := range m {
		size += len(key) + estimateAnyMarshalSize(value, depth+1)
	}
	return size
}

func estimateReflectMarshalSize(v reflect.Value, depth int) int {
	v = indirectValue(v)
	if !v.IsValid() {
		return 0
	}
	if depth > 8 {
		return 64
	}
	if isScalarSpecial(v) {
		return 40
	}
	switch v.Kind() {
	case reflect.String:
		return len(v.String()) + 2
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return 24
	case reflect.Slice, reflect.Array:
		return estimateReflectSliceMarshalSize(v, depth+1)
	case reflect.Map:
		return estimateReflectMapMarshalSize(v, depth+1)
	case reflect.Struct:
		return estimateReflectStructMarshalSize(v, depth+1)
	case reflect.Interface:
		if v.IsNil() {
			return 0
		}
		return estimateReflectMarshalSize(v.Elem(), depth+1)
	default:
		return 0
	}
}

func estimateReflectSliceMarshalSize(v reflect.Value, depth int) int {
	length := v.Len()
	if length == 0 {
		return 2
	}
	limit := min(length, 8)
	sum := 0
	for i := range limit {
		sum += estimateReflectMarshalSize(v.Index(i), depth+1) + 2
	}
	return 2 + length*max(1, sum/limit)
}

func estimateReflectMapMarshalSize(v reflect.Value, depth int) int {
	if v.Type().Key().Kind() != reflect.String {
		return 0
	}
	size := v.Len() * 6
	iter := v.MapRange()
	for iter.Next() {
		size += len(iter.Key().String()) + estimateReflectMarshalSize(iter.Value(), depth+1)
	}
	return size
}

func estimateReflectStructMarshalSize(v reflect.Value, depth int) int {
	info, err := reflectcache.Lookup(v.Type())
	if err != nil {
		return v.NumField() * 32
	}
	size := len(info.MarshalFields) * 6
	for _, field := range info.MarshalFields {
		fv, ok := marshalFieldValue(v, field)
		if !ok {
			continue
		}
		size += len(field.Name) + estimateReflectMarshalSize(fv, depth+1)
	}
	return size
}

func structMarshalEntries(v reflect.Value) ([]marshalEntry, error) {
	info, err := reflectcache.Lookup(v.Type())
	if err != nil {
		return nil, normalizeReflectcacheError(err)
	}
	entries := make([]marshalEntry, 0, len(info.Fields))
	for _, f := range info.Fields {
		fv, ok := marshalFieldValue(v, f)
		if !ok {
			continue
		}
		entry := marshalEntry{name: f.Name, value: fv}
		if i := findMarshalEntry(entries, f.Name); i >= 0 {
			entries[i] = entry
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})
	return entries, nil
}

func marshalFieldValue(v reflect.Value, field reflectcache.Field) (reflect.Value, bool) {
	fv := v.FieldByIndex(field.Index)
	if field.OmitZero && fv.IsZero() {
		return reflect.Value{}, false
	}
	if !fv.CanInterface() {
		return reflect.Value{}, false
	}
	if isNilValue(fv) {
		return reflect.Value{}, false
	}
	return fv, true
}

func findMarshalEntry(entries []marshalEntry, name string) int {
	for i, entry := range entries {
		if entry.name == name {
			return i
		}
	}
	return -1
}

// sortedMapEntries extracts every key/value pair of the string-keyed map v
// in one MapRange pass and returns them sorted by key name, so callers never
// need a second, separately-hashed MapIndex lookup per key.
func sortedMapEntries(v reflect.Value) ([]marshalEntry, error) {
	if v.Type().Key().Kind() != reflect.String {
		return nil, &UnsupportedTypeError{Type: v.Type().String()}
	}
	entries := make([]marshalEntry, 0, v.Len())
	for iter := v.MapRange(); iter.Next(); {
		entries = append(entries, marshalEntry{name: iter.Key().String(), value: iter.Value()})
	}
	slices.SortFunc(entries, func(a, b marshalEntry) int {
		return cmp.Compare(a.name, b.name)
	})
	return entries, nil
}

func sortedStringKeys(m map[string]any) []string {
	keysp, _ := stringKeysPool.Get().(*[]string)
	var keys []string
	if keysp != nil {
		keys = *keysp
	}
	if cap(keys) < len(m) {
		keys = make([]string, 0, len(m))
	} else {
		keys = keys[:0]
	}
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func recycleStringKeys(keys []string) {
	if cap(keys) > maxPooledStringKeys {
		return
	}
	clear(keys)
	keys = keys[:0]
	stringKeysPool.Put(&keys)
}

func appendPath(path []string, key string) []string {
	next := make([]string, len(path)+1)
	copy(next, path)
	next[len(path)] = key
	return next
}

func isTableLike(v reflect.Value) bool {
	v = indirectValue(v)
	if !v.IsValid() || isScalarSpecial(v) {
		return false
	}
	if isArrayOfTables(v) {
		return true
	}
	return v.Kind() == reflect.Struct || v.Kind() == reflect.Map
}

func isTableLikeAny(value any) bool {
	switch x := value.(type) {
	case nil:
		return false
	case time.Time, LocalDateTime, LocalDate, LocalTime, encoding.TextMarshaler:
		return false
	case map[string]any:
		return true
	case []any:
		return isArrayOfTablesAny(x)
	default:
		return isTableLike(reflect.ValueOf(value))
	}
}

func isScalarSpecial(v reflect.Value) bool {
	return v.IsValid() && v.CanInterface() && isScalarSpecialType(v.Type())
}

// specialKind classifies a reflect.Type for TOML scalar encoding purposes.
type specialKind uint8

const (
	specialKindNone specialKind = iota
	specialKindTime
	specialKindLocalDateTime
	specialKindLocalDate
	specialKindLocalTime
	specialKindTextMarshaler
)

// specialKindCache memoizes computeSpecialKind per reflect.Type, so the
// encoding.TextMarshaler method-set scan runs once per type instead of once
// per encoded value.
var specialKindCache sync.Map // map[reflect.Type]specialKind

func lookupSpecialKind(t reflect.Type) specialKind {
	if kind, ok := specialKindCache.Load(t); ok {
		return kind.(specialKind)
	}
	kind := computeSpecialKind(t)
	if actual, loaded := specialKindCache.LoadOrStore(t, kind); loaded {
		return actual.(specialKind)
	}
	return kind
}

func computeSpecialKind(t reflect.Type) specialKind {
	switch t {
	case timeType:
		return specialKindTime
	case localDateTimeType:
		return specialKindLocalDateTime
	case localDateType:
		return specialKindLocalDate
	case localTimeType:
		return specialKindLocalTime
	}
	if t.PkgPath() != "" && t.Implements(textMarshalerType) {
		return specialKindTextMarshaler
	}
	return specialKindNone
}

func isScalarSpecialType(t reflect.Type) bool {
	return lookupSpecialKind(t) != specialKindNone
}

func isArrayOfTables(v reflect.Value) bool {
	v = indirectValue(v)
	if !v.IsValid() || (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) || v.Len() == 0 {
		return false
	}
	e := indirectValue(v.Index(0))
	return e.IsValid() && !isScalarSpecial(e) && (e.Kind() == reflect.Struct || e.Kind() == reflect.Map)
}

func isArrayOfTablesAny(value any) bool {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return false
	}
	allMaps := true
	allEmpty := true
	hasNestedTable := false
	for _, item := range items {
		switch x := item.(type) {
		case map[string]any:
			if len(x) != 0 {
				allEmpty = false
				if containsNonEmptyTableLikeValueAny(x) {
					hasNestedTable = true
				}
			}
		default:
			allMaps = false
		}
		if !allMaps {
			return false
		}
	}
	return allEmpty || hasNestedTable
}

func containsNonEmptyTableLikeValueAny(v map[string]any) bool {
	for _, child := range v {
		if hasNonEmptyTableLikeDescendantAny(child) {
			return true
		}
	}
	return false
}

func hasNonEmptyTableLikeDescendantAny(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return false
		}
		return containsNonEmptyTableLikeValueAny(x)
	case []any:
		return slices.ContainsFunc(x, hasNonEmptyTableLikeDescendantAny)
	default:
		return false
	}
}

func formatKey(key string) (string, error) {
	if key == "" {
		return `""`, nil
	}
	for _, r := range key {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		b, err := appendBasicString(make([]byte, 0, len(key)+2), key)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return key, nil
}
