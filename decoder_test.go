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
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDecoderDecode(t *testing.T) {
	t.Parallel()

	type config struct {
		Name   string
		Server struct {
			Port int
		}
	}

	tests := map[string]struct {
		newDecoder func() *Decoder
		want       config
	}{
		"success: reader decoder decodes struct": {
			newDecoder: func() *Decoder {
				return NewDecoder(strings.NewReader("name = \"demo\"\n[server]\nport = 9443\n"))
			},
			want: config{Name: "demo", Server: struct {
				Port int
			}{Port: 9443}},
		},
		"success: byte decoder decodes struct": {
			newDecoder: func() *Decoder {
				return NewDecoderBytes([]byte("name = \"bytes\"\n[server]\nport = 8080\n"))
			},
			want: config{Name: "bytes", Server: struct {
				Port int
			}{Port: 8080}},
		},
		"success: bom-prefixed fresh decoder decodes struct": {
			newDecoder: func() *Decoder {
				return NewDecoder(strings.NewReader("\ufeffname = \"bom\"\n[server]\nport = 443\n"))
			},
			want: config{Name: "bom", Server: struct {
				Port int
			}{Port: 443}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got config
			if err := tc.newDecoder().Decode(&got); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Decode() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDecoderDecodePropagatesOptions(t *testing.T) {
	t.Parallel()

	type config struct {
		When time.Time
	}

	var got config
	dec := NewDecoder(
		strings.NewReader("when = 2026-05-20T01:02:03\n"),
		WithLocalAsUTC(),
		WithLimits(Limits{MaxDocumentSize: 64}),
	)
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.When.Location() != time.UTC {
		t.Fatalf("When.Location() = %v, want UTC", got.When.Location())
	}
	if got.When.Format(time.RFC3339) != "2026-05-20T01:02:03Z" {
		t.Fatalf("When = %s, want 2026-05-20T01:02:03Z", got.When.Format(time.RFC3339Nano))
	}

	var tooLarge config
	err := NewDecoder(
		strings.NewReader("when = 2026-05-20T01:02:03\n"),
		WithLimits(Limits{MaxDocumentSize: 4}),
	).Decode(&tooLarge)
	var limitErr *LimitError
	if !errors.As(err, &limitErr) || limitErr.Limit != "MaxDocumentSize" {
		t.Fatalf("Decode() error = %T(%v), want MaxDocumentSize LimitError", err, err)
	}
}

func TestDecoderDecodeErrors(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string
	}

	tests := map[string]struct {
		dec      *Decoder
		dst      any
		checkErr func(testing.TB, error)
	}{
		"error: nil destination matches Unmarshal": {
			dec: NewDecoder(strings.NewReader("name = \"demo\"\n")),
			dst: nil,
			checkErr: func(t testing.TB, err error) {
				t.Helper()
				var mismatch *TypeMismatchError
				if !errors.As(err, &mismatch) || mismatch.Want != "non-nil pointer" || mismatch.Got != "nil" {
					t.Fatalf("Decode() error = %T(%v), want nil destination TypeMismatchError", err, err)
				}
			},
		},
		"error: non-pointer destination matches Unmarshal": {
			dec: NewDecoder(strings.NewReader("name = \"demo\"\n")),
			dst: config{},
			checkErr: func(t testing.TB, err error) {
				t.Helper()
				var mismatch *TypeMismatchError
				if !errors.As(err, &mismatch) || mismatch.Want != "non-nil pointer" || mismatch.Got != "struct" {
					t.Fatalf("Decode() error = %T(%v), want non-pointer TypeMismatchError", err, err)
				}
			},
		},
		"error: nil decoder": {
			dec: nil,
			dst: &config{},
			checkErr: func(t testing.TB, err error) {
				t.Helper()
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("Decode() error = %T(%v), want io.ErrUnexpectedEOF", err, err)
				}
			},
		},
		"error: invalid utf-8 before destination validation": {
			dec: NewDecoderBytes([]byte{0xff}),
			dst: nil,
			checkErr: func(t testing.TB, err error) {
				t.Helper()
				var syntaxErr *SyntaxError
				if !errors.As(err, &syntaxErr) || syntaxErr.Msg != "invalid utf-8" {
					t.Fatalf("Decode() error = %T(%v), want invalid utf-8 SyntaxError", err, err)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.checkErr(t, tc.dec.Decode(tc.dst))
		})
	}
}

func TestDecoderDecodeReturnsReaderErrorFirst(t *testing.T) {
	t.Parallel()

	want := errors.New("reader failed")
	dec := NewDecoder(errorReader{err: want})
	var dst struct{}
	if err := dec.Decode(&dst); !errors.Is(err, want) {
		t.Fatalf("Decode() error = %T(%v), want reader error %v", err, err, want)
	}
}

func TestDecoderDecodeRejectsConsumedTokenStream(t *testing.T) {
	t.Parallel()

	dec := NewDecoder(strings.NewReader("name = \"demo\"\n"))
	if _, err := dec.ReadToken(); err != nil {
		t.Fatalf("ReadToken() error = %v", err)
	}

	var dst struct {
		Name string
	}
	var stateErr *DecoderStateError
	if err := dec.Decode(&dst); !errors.As(err, &stateErr) {
		t.Fatalf("Decode() error = %T(%v), want DecoderStateError", err, err)
	}
	if stateErr.Offset == 0 {
		t.Fatalf("DecoderStateError.Offset = 0, want consumed offset")
	}
}

func TestDecoderDecodeConsumesDecoder(t *testing.T) {
	t.Parallel()

	dec := NewDecoder(strings.NewReader("name = \"demo\"\n"))
	var dst struct {
		Name string
	}
	if err := dec.Decode(&dst); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if dst.Name != "demo" {
		t.Fatalf("Name = %q, want demo", dst.Name)
	}

	var second struct {
		Name string
	}
	var stateErr *DecoderStateError
	if err := dec.Decode(&second); !errors.As(err, &stateErr) {
		t.Fatalf("second Decode() error = %T(%v), want DecoderStateError", err, err)
	}
	if stateErr.Offset != len("name = \"demo\"\n") {
		t.Fatalf("DecoderStateError.Offset = %d, want %d", stateErr.Offset, len("name = \"demo\"\n"))
	}
	if second.Name != "" {
		t.Fatalf("second Decode() mutated destination to %q, want empty", second.Name)
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadToken() after Decode() error = %T(%v), want io.EOF", err, err)
	}
}

func TestDecoderDecodeCustomUnmarshalerFrom(t *testing.T) {
	t.Parallel()

	var dst decoderDecodeCustom
	if err := NewDecoder(strings.NewReader("name = \"ignored\"\n")).Decode(&dst); err != nil {
		t.Fatalf("Decode(custom) error = %v", err)
	}
	if !dst.decoded {
		t.Fatalf("Decode(custom) did not call UnmarshalTOMLFrom")
	}
}

type decoderDecodeCustom struct {
	decoded bool
}

func (c *decoderDecodeCustom) UnmarshalTOMLFrom(dec *Decoder) error {
	for {
		_, err := dec.ReadToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	c.decoded = true
	return nil
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
