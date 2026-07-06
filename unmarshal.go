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
	"encoding"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zchee/go-toml/internal/reflectcache"
)

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

var decodeFilterCache sync.Map // map[reflect.Type]*decodeFilter

// Unmarshal decodes a TOML document into dst.
//
// By default, decoded string values alias data directly (zero copy). Callers
// MUST NOT mutate data while any decoded string remains in use, and MUST NOT
// retain decoded strings past a subsequent mutation of data. Escaped strings,
// which require unescaping, are always independently allocated. To force every
// decoded string into an independent allocation, decode with a Decoder
// configured via WithCopiedStrings instead.
func Unmarshal(data []byte, dst any) error {
	return unmarshalWithOptions(data, dst, UnmarshalOptions{})
}

func unmarshalWithOptions(data []byte, dst any, opts UnmarshalOptions) error {
	if dst == nil {
		return &TypeMismatchError{Want: "non-nil pointer", Got: "nil"}
	}
	if u, ok := dst.(UnmarshalerFrom); ok {
		return u.UnmarshalTOMLFrom(NewDecoderBytes(data, opts.DecoderOptions...))
	}
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return &TypeMismatchError{Want: "non-nil pointer", Got: v.Kind().String()}
	}
	directEligible, err := directStructUnmarshalEligible(v.Elem().Type())
	if err != nil {
		return err
	}
	if directEligible {
		cfg := bindConfigFromOptions(opts.DecoderOptions)
		return bindDocumentDirect(data, v.Elem(), opts.DecoderOptions, cfg)
	}
	filter, err := newDecodeFilter(v.Elem().Type())
	if err != nil {
		return normalizeReflectcacheError(err)
	}
	root, err := parseDocument(data, opts.DecoderOptions, filter)
	if err != nil {
		return err
	}
	if canRecycleDocumentFor(v.Elem().Type()) {
		defer recycleDocument(root)
	}
	cfg := bindConfigFromOptions(opts.DecoderOptions)
	return bindValue(v.Elem(), root, cfg)
}

type bindConfig struct {
	localAsUTC  bool
	copyStrings bool
}

func bindConfigFromOptions(opts []Option) bindConfig {
	if len(opts) == 0 {
		return bindConfig{}
	}
	decoder := &Decoder{}
	for _, opt := range opts {
		opt(decoder)
	}
	return bindConfig{localAsUTC: decoder.localAsUTC, copyStrings: decoder.copyStrings}
}

func canRecycleDocumentFor(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[time.Time]() ||
		t == reflect.TypeFor[LocalDateTime]() ||
		t == reflect.TypeFor[LocalDate]() ||
		t == reflect.TypeFor[LocalTime]() {
		return true
	}
	switch t.Kind() {
	case reflect.Interface, reflect.Map:
		return false
	case reflect.Struct:
		for sf := range t.Fields() {
			if sf.PkgPath != "" && !sf.Anonymous {
				continue
			}
			if !canRecycleDocumentFor(sf.Type) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		return canRecycleDocumentFor(t.Elem())
	default:
		return true
	}
}

type decodeFilter struct {
	children map[string]*decodeFilter
}

//nolint:cyclop // dispatch over reflect kinds to build the field filter; cohesive.
func newDecodeFilter(t reflect.Type) (*decodeFilter, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if cached, ok := decodeFilterCache.Load(t); ok {
		return cached.(*decodeFilter), nil
	}
	switch t.Kind() {
	case reflect.Interface, reflect.Map:
		return nil, nil //nolint:nilnil // a nil filter signals "no field restriction"; see decodeFilter.lookup.
	case reflect.Slice, reflect.Array:
		return newDecodeFilter(t.Elem())
	case reflect.Struct:
		if t == reflect.TypeFor[time.Time]() ||
			t == reflect.TypeFor[LocalDateTime]() ||
			t == reflect.TypeFor[LocalDate]() ||
			t == reflect.TypeFor[LocalTime]() {
			return nil, nil //nolint:nilnil // a nil filter signals "no field restriction"; see decodeFilter.lookup.
		}
		info, err := reflectcache.Lookup(t)
		if err != nil {
			return nil, err
		}
		filter := &decodeFilter{children: make(map[string]*decodeFilter, len(info.Fields)*2)}
		for _, field := range info.Fields {
			child, err := newDecodeFilter(field.Type)
			if err != nil {
				return nil, err
			}
			filter.children[field.Name] = child
			lower := strings.ToLower(field.Name)
			if _, exists := filter.children[lower]; !exists {
				filter.children[lower] = child
			}
		}
		if cached, loaded := decodeFilterCache.LoadOrStore(t, filter); loaded {
			return cached.(*decodeFilter), nil
		}
		return filter, nil
	default:
		return nil, nil //nolint:nilnil // a nil filter signals "no field restriction"; see decodeFilter.lookup.
	}
}

func (f *decodeFilter) lookup(raw []byte) (*decodeFilter, bool) {
	if f == nil {
		return nil, true
	}
	if len(f.children) == 0 {
		return nil, false
	}
	if isSimpleBareKey(raw) {
		child, ok := f.children[string(raw)]
		return child, ok
	}
	path, err := parseDottedKey(raw)
	if err != nil {
		return nil, true
	}
	return f.lookupPath(path)
}

func (f *decodeFilter) lookupPath(path []string) (*decodeFilter, bool) {
	if f == nil {
		return nil, true
	}
	cur := f
	for _, part := range path {
		next, ok := cur.children[part]
		if !ok {
			next, ok = cur.children[strings.ToLower(part)]
		}
		if !ok {
			return nil, false
		}
		if next == nil {
			return nil, true
		}
		cur = next
	}
	return cur, true
}

//nolint:cyclop,funlen,gocognit,gocyclo // core reflect value binder dispatching over every target kind; cohesive.
func bindValue(dst reflect.Value, src any, cfg bindConfig) error {
	if !dst.CanSet() {
		return nil
	}
	if dst.Kind() == reflect.Pointer {
		if src == nil {
			return nil
		}
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return bindValue(dst.Elem(), src, cfg)
	}
	if dst.Type() == reflect.TypeFor[time.Time]() {
		switch v := src.(type) {
		case time.Time:
			dst.Set(reflect.ValueOf(v))
			return nil
		case LocalDateTime, LocalDate, LocalTime:
			if !cfg.localAsUTC {
				return &LocalTimeIntoTimeError{Kind: TokenKindValueDatetime}
			}
			switch x := v.(type) {
			case LocalDateTime:
				dst.Set(reflect.ValueOf(time.Date(x.Year, time.Month(x.Month), x.Day, x.Hour, x.Minute, x.Second, x.Nanosecond, time.UTC)))
			case LocalDate:
				dst.Set(reflect.ValueOf(time.Date(x.Year, time.Month(x.Month), x.Day, 0, 0, 0, 0, time.UTC)))
			case LocalTime:
				dst.Set(reflect.ValueOf(time.Date(0, time.January, 1, x.Hour, x.Minute, x.Second, x.Nanosecond, time.UTC)))
			}
			return nil
		}
	}
	if dst.CanAddr() && dst.Addr().Type().Implements(textUnmarshalerType) { //nolint:nestif // text-unmarshaler fallback for non-string sources; cohesive.
		text, ok := src.(string)
		if !ok {
			if tm, isMarshaler := src.(encoding.TextMarshaler); isMarshaler {
				b, err := tm.MarshalText()
				if err != nil {
					return err
				}
				text = string(b)
				ok = true
			}
		}
		if ok {
			return dst.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(text))
		}
	}
	switch dst.Kind() {
	case reflect.Interface:
		dst.Set(reflect.ValueOf(publicAnyValue(src)))
		return nil
	case reflect.Struct:
		m, ok := asDocumentMap(src)
		if !ok {
			return mismatch(dst.Type(), src)
		}
		info, err := reflectcache.Lookup(dst.Type())
		if err != nil {
			return normalizeReflectcacheError(err)
		}
		for name, raw := range m {
			field, ok := lookupStructField(info, name)
			if !ok {
				continue
			}
			if err := bindValue(dst.FieldByIndex(field.Index), raw, cfg); err != nil {
				return bindErrorPath(err, name)
			}
		}
		return nil
	case reflect.Map:
		m, ok := asDocumentMap(src)
		if !ok {
			return mismatch(dst.Type(), src)
		}
		if dst.Type().Key().Kind() != reflect.String {
			return &UnsupportedTypeError{Type: dst.Type().String()}
		}
		if dst.Type().Elem().Kind() == reflect.Interface && dst.Len() == 0 {
			source := reflect.ValueOf(publicAnyMap(m))
			if source.Type().AssignableTo(dst.Type()) {
				dst.Set(source)
				return nil
			}
			if source.Type().ConvertibleTo(dst.Type()) {
				dst.Set(source.Convert(dst.Type()))
				return nil
			}
		}
		if dst.IsNil() {
			dst.Set(reflect.MakeMapWithSize(dst.Type(), len(m)))
		}
		for k, raw := range m {
			kv := reflect.ValueOf(k).Convert(dst.Type().Key())
			ev := reflect.New(dst.Type().Elem()).Elem()
			if err := bindValue(ev, raw, cfg); err != nil {
				return bindErrorPath(err, k)
			}
			dst.SetMapIndex(kv, ev)
		}
		return nil
	case reflect.Slice:
		items, ok := src.([]any)
		if !ok {
			return mismatch(dst.Type(), src)
		}
		out := reflect.MakeSlice(dst.Type(), len(items), len(items))
		for i, raw := range items {
			if err := bindValue(out.Index(i), raw, cfg); err != nil {
				return bindErrorPath(err, indexPath(i))
			}
		}
		dst.Set(out)
		return nil
	case reflect.Array:
		items, ok := src.([]any)
		if !ok || len(items) != dst.Len() {
			return mismatch(dst.Type(), src)
		}
		for i, raw := range items {
			if err := bindValue(dst.Index(i), raw, cfg); err != nil {
				return bindErrorPath(err, indexPath(i))
			}
		}
		return nil
	case reflect.String:
		s, ok := src.(string)
		if !ok {
			return mismatch(dst.Type(), src)
		}
		dst.SetString(s)
		return nil
	case reflect.Bool:
		b, ok := src.(bool)
		if !ok {
			return mismatch(dst.Type(), src)
		}
		dst.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, ok := int64Value(src)
		if !ok || dst.OverflowInt(i) {
			return mismatch(dst.Type(), src)
		}
		dst.SetInt(i)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		i, ok := int64Value(src)
		if !ok || i < 0 || dst.OverflowUint(uint64(i)) {
			return mismatch(dst.Type(), src)
		}
		dst.SetUint(uint64(i))
		return nil
	case reflect.Float32, reflect.Float64:
		f, ok := float64Value(src)
		if !ok || dst.OverflowFloat(f) {
			return mismatch(dst.Type(), src)
		}
		dst.SetFloat(f)
		return nil
	default:
		return &UnsupportedTypeError{Type: dst.Type().String()}
	}
}

func asDocumentMap(v any) (documentMap, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func publicAnyValue(v any) any {
	return v
}

func publicAnyMap(m map[string]any) map[string]any {
	return m
}

func int64Value(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	default:
		return 0, false
	}
}

func float64Value(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}

func mismatch(typ reflect.Type, src any) error {
	return &TypeMismatchError{Want: typ.String(), Got: typeName(src)}
}

func typeName(src any) string {
	if src == nil {
		return "<nil>"
	}
	return reflect.TypeOf(src).String()
}

func lookupStructField(info *reflectcache.TypeInfo, name string) (reflectcache.Field, bool) {
	if field, ok := info.ByName[name]; ok {
		return field, true
	}
	for _, field := range info.Fields {
		if strings.EqualFold(field.Name, name) {
			return field, true
		}
	}
	return reflectcache.Field{}, false
}

func bindErrorPath(err error, prefix string) error {
	if err == nil || prefix == "" {
		return err
	}
	if tm, ok := errors.AsType[*TypeMismatchError](err); ok {
		tm.Path = joinPathComponent(prefix, tm.Path)
	}
	return err
}

func joinPathComponent(prefix, suffix string) string {
	if suffix == "" {
		return prefix
	}
	if prefix == "" {
		return suffix
	}
	if suffix[0] == '[' {
		return prefix + suffix
	}
	return prefix + "." + suffix
}

func indexPath(i int) string {
	var buf [32]byte
	b := buf[:0]
	b = append(b, '[')
	b = strconv.AppendInt(b, int64(i), 10)
	b = append(b, ']')
	return string(b)
}

func normalizeReflectcacheError(err error) error {
	if tag, ok := errors.AsType[*reflectcache.InvalidTagOptionError](err); ok {
		return &TagOptionError{Struct: tag.Struct.String(), Field: tag.Field, Option: tag.Option}
	}
	return err
}
