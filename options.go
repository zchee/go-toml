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

// MarshalOptions controls high-level TOML encoding.
type MarshalOptions struct{}

// UnmarshalOptions controls high-level TOML decoding.
type UnmarshalOptions struct {
	// DecoderOptions are passed to the streaming Decoder used by Unmarshal.
	DecoderOptions []Option
}

// WithDefaultOptions returns explicit facade defaults for call sites that want
// stable option values instead of relying on zero values.
func WithDefaultOptions() (MarshalOptions, UnmarshalOptions) {
	return MarshalOptions{}, UnmarshalOptions{}
}

// Marshal encodes v with o.
func (o MarshalOptions) Marshal(v any) ([]byte, error) {
	return marshalWithOptions(v, o)
}

// Unmarshal decodes data into dst with o.
func (o UnmarshalOptions) Unmarshal(data []byte, dst any) error {
	return unmarshalWithOptions(data, dst, o)
}
