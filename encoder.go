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
	"io"
)

// MarshalerTo is the fast-path TOML encoder hook.
type MarshalerTo interface {
	MarshalTOMLTo(enc *Encoder) error
}

// UnmarshalerFrom is the fast-path TOML decoder hook.
type UnmarshalerFrom interface {
	UnmarshalTOMLFrom(dec *Decoder) error
}

// Encoder writes one TOML document to an io.Writer.
type Encoder struct {
	w    io.Writer
	opts MarshalOptions
}

// NewEncoder returns an encoder writing to w.
func NewEncoder(w io.Writer, opts ...MarshalOptions) *Encoder {
	enc := &Encoder{w: w}
	if len(opts) > 0 {
		enc.opts = opts[len(opts)-1]
	}
	return enc
}

// Encode writes v as a TOML document.
func (e *Encoder) Encode(v any) error {
	if e == nil || e.w == nil {
		return &UnsupportedTypeError{Type: "<nil writer>"}
	}
	if m, ok := v.(MarshalerTo); ok {
		return m.MarshalTOMLTo(e)
	}
	if buf, ok := e.w.(*bytes.Buffer); ok {
		return marshalToBuffer(buf, v, e.opts)
	}
	b, err := marshalWithOptions(v, e.opts)
	if err != nil {
		return err
	}
	_, err = e.w.Write(b)
	return err
}

// Write writes raw TOML bytes to the underlying writer. It is provided for
// MarshalerTo implementations that already own their document encoding.
func (e *Encoder) Write(p []byte) (int, error) {
	if e == nil || e.w == nil {
		return 0, &UnsupportedTypeError{Type: "<nil writer>"}
	}
	return e.w.Write(p)
}
