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

// Package testsuite provides helper functions for interoperating with the
// language-agnostic TOML test suite at https://github.com/toml-lang/toml-test.
package testsuite

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/zchee/go-toml"
)

// Marshal is a helper function for calling toml.Marshal
//
// Only needed to avoid package import loops.
func Marshal(v any) ([]byte, error) {
	return toml.Marshal(v)
}

// Unmarshal is a helper function for calling toml.Unmarshal.
//
// Only needed to avoid package import loops.
func Unmarshal(data []byte, v any) error {
	return toml.Unmarshal(data, v)
}

// ValueToTaggedJSON takes a data structure and returns the tagged JSON
// representation.
func ValueToTaggedJSON(doc any) ([]byte, error) {
	tagged, err := addTag(doc)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(tagged, "", "  ")
}

// DecodeStdin is a helper function for the toml-test binary interface.  TOML input
// is read from STDIN and a resulting tagged JSON representation is written to
// STDOUT.
func DecodeStdin() error {
	var decoded map[string]any

	if err := toml.NewDecoder(os.Stdin).Decode(&decoded); err != nil {
		return fmt.Errorf("error decoding TOML: %w", err)
	}

	j := json.NewEncoder(os.Stdout)
	j.SetIndent("", "  ")
	tagged, err := addTag(decoded)
	if err != nil {
		return fmt.Errorf("tagging decoded TOML: %w", err)
	}
	if err := j.Encode(tagged); err != nil {
		return fmt.Errorf("error encoding JSON: %w", err)
	}

	return nil
}

// EncodeStdin is a helper function for the toml-test binary interface.  Tagged
// JSON is read from STDIN and a resulting TOML representation is written to
// STDOUT.
func EncodeStdin() error {
	var j any
	err := json.NewDecoder(os.Stdin).Decode(&j)
	if err != nil {
		return err
	}

	rm, err := rmTag(j)
	if err != nil {
		return fmt.Errorf("removing tags: %w", err)
	}

	return toml.NewEncoder(os.Stdout).Encode(rm)
}
