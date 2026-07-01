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

package benchmark_test

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	burntsushi "github.com/BurntSushi/toml"
	pelletier "github.com/pelletier/go-toml/v2"

	simdtoml "github.com/zchee/go-toml"
)

var benchPackageFlag = flag.String("package", "package", "package name")

type encoderFunc func(w io.Writer) func(v any) error

var encoderFactories = map[string]encoderFunc{
	"burntsushi": func(w io.Writer) func(any) error {
		enc := burntsushi.NewEncoder(w)
		return enc.Encode
	},
	"pelletier": func(w io.Writer) func(any) error {
		enc := pelletier.NewEncoder(w)
		return enc.Encode
	},
	"simdtoml": func(w io.Writer) func(any) error {
		enc := simdtoml.NewEncoder(w)
		return enc.Encode
	},
}

func newEncoder(v any) ([]byte, error) {
	encoder, ok := encoderFactories[*benchPackageFlag]
	if !ok {
		panic(fmt.Sprintf("unknown package: %q", *benchPackageFlag))
	}

	b := new(bytes.Buffer)
	enc := encoder(b)
	err := enc(v)

	return b.Bytes(), err
}

type unmarshalFunc func([]byte, any) error

var unmarshalerFactories = map[string]unmarshalFunc{
	"burntsushi": burntsushi.Unmarshal,
	"pelletier":  pelletier.Unmarshal,
	"simdtoml":   simdtoml.Unmarshal,
}

func newUnmarshaler(data []byte, v any) error {
	unmarshaler, ok := unmarshalerFactories[*benchPackageFlag]
	if !ok {
		panic(fmt.Sprintf("unknown package: %q", *benchPackageFlag))
	}
	return unmarshaler(data, v)
}

func BenchmarkMarshal(b *testing.B) {
	b.Run("Document", func(b *testing.B) {
		data := []byte(`A = "hello"`)

		b.Run("struct", func(b *testing.B) {
			v := struct {
				A string
			}{}
			err := newUnmarshaler(data, &v)
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			var out []byte
			for b.Loop() {
				out, err = newEncoder(v)
				if err != nil {
					b.Error(err)
				}
			}
			b.SetBytes(int64(len(out)))
		})

		b.Run("map", func(b *testing.B) {
			v := map[string]any{}
			err := newUnmarshaler(data, &v)
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			var out []byte
			for b.Loop() {
				out, err = newEncoder(v)
				if err != nil {
					b.Error(err)
				}
			}
			b.SetBytes(int64(len(out)))
		})
	})

	b.Run("File", func(b *testing.B) {
		data, err := os.ReadFile("testdata/benchmark.toml")
		if err != nil {
			b.Fatal(err)
		}

		b.Run("struct", func(b *testing.B) {
			v := benchmarkDoc{}
			err := newUnmarshaler(data, &v)
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			var out []byte
			for b.Loop() {
				out, err = newEncoder(v)
				if err != nil {
					b.Error(err)
				}
			}
			b.SetBytes(int64(len(out)))
		})

		b.Run("map", func(b *testing.B) {
			v := map[string]any{}
			err := newUnmarshaler(data, &v)
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			var out []byte
			for b.Loop() {
				out, err = newEncoder(v)
				if err != nil {
					b.Error(err)
				}
			}
			b.SetBytes(int64(len(out)))
		})
	})

	b.Run("Hugo", func(b *testing.B) {
		v := map[string]any{}
		err := newUnmarshaler(hugoFrontMatterbytes, &v)
		if err != nil {
			b.Fatal(err)
		}

		b.ReportAllocs()
		var out []byte
		for b.Loop() {
			out, err = newEncoder(v)
			if err != nil {
				b.Error(err)
			}
		}
		b.SetBytes(int64(len(out)))
	})
}

var benchUnmarshalSink any

func BenchmarkUnmarshal(b *testing.B) {
	b.Run("Document", func(b *testing.B) {
		data := []byte(`A = "hello"`)

		b.Run("struct", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				v := struct {
					A string
				}{}
				err := newUnmarshaler(data, &v)
				if err != nil {
					b.Error(err)
				}
				benchUnmarshalSink = v
			}
		})

		b.Run("map", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				v := map[string]any{}
				err := newUnmarshaler(data, &v)
				if err != nil {
					b.Error(err)
				}
				benchUnmarshalSink = v
			}
		})
	})

	b.Run("ReferenceFile", func(b *testing.B) {
		bytes, err := os.ReadFile("testdata/benchmark.toml")
		if err != nil {
			b.Fatal(err)
		}

		b.Run("struct", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(bytes)))
			for b.Loop() {
				v := benchmarkDoc{}
				err := newUnmarshaler(bytes, &v)
				if err != nil {
					b.Error(err)
				}
				benchUnmarshalSink = v
			}
		})

		b.Run("map", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(bytes)))
			for b.Loop() {
				v := map[string]any{}
				err := newUnmarshaler(bytes, &v)
				if err != nil {
					b.Error(err)
				}
				benchUnmarshalSink = v
			}
		})
	})

	b.Run("Hugo", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(hugoFrontMatterbytes)))
		for b.Loop() {
			v := map[string]any{}
			err := newUnmarshaler(hugoFrontMatterbytes, &v)
			if err != nil {
				b.Error(err)
			}
			benchUnmarshalSink = v
		}
	})
}

var hugoFrontMatterbytes = []byte(`
categories = ["Development", "VIM"]
date = "2012-04-06"
description = "spf13-vim is a cross platform distribution of vim plugins and resources for Vim."
slug = "spf13-vim-3-0-release-and-new-website"
tags = [".vimrc", "plugins", "spf13-vim", "vim"]
title = "spf13-vim 3.0 release and new website"
include_toc = true
show_comments = false

[[cascade]]
  background = "yosemite.jpg"
  [cascade._target]
    kind = "page"
    lang = "en"
    path = "/blog/**"

[[cascade]]
  background = "goldenbridge.jpg"
  [cascade._target]
    kind = "section"
`)

type benchmarkDoc struct {
	Table struct {
		Key      string
		Subtable struct {
			Key string
		}
		Inline struct {
			Name struct {
				First string
				Last  string
			}
			Point struct {
				X int64
				Y int64
			}
		}
	}
	String struct {
		Basic struct {
			Basic string
		}
		Multiline struct {
			Key1      string
			Key2      string
			Key3      string
			Continued struct {
				Key1 string
				Key2 string
				Key3 string
			}
		}
		Literal struct {
			Winpath   string
			Winpath2  string
			Quoted    string
			Regex     string
			Multiline struct {
				Regex2 string
				Lines  string
			}
		}
	}
	Integer struct {
		Key1        int64
		Key2        int64
		Key3        int64
		Key4        int64
		Underscores struct {
			Key1 int64
			Key2 int64
			Key3 int64
		}
	}
	Float struct {
		Fractional struct {
			Key1 float64
			Key2 float64
			Key3 float64
		}
		Exponent struct {
			Key1 float64
			Key2 float64
			Key3 float64
		}
		Both struct {
			Key float64
		}
		Underscores struct {
			Key1 float64
			Key2 float64
		}
	}
	Boolean struct {
		True  bool
		False bool
	}
	Datetime struct {
		Key1 time.Time
		Key2 time.Time
		Key3 time.Time
	}
	Array struct {
		Key1 []int64
		Key2 []string
		Key3 [][]int64
		// TODO: Key4 not supported by go-toml's Unmarshal
		Key4 any
		Key5 []int64
		Key6 []int64
	}
	Products []struct {
		Name  string
		Sku   int64
		Color string
	}
	Fruit []struct {
		Name     string
		Physical struct {
			Color string
			Shape string
		}
		Variety []struct {
			Name string
		}
	}
}
