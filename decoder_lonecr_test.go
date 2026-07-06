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
	"testing"
	"time"
)

// TestDecoderBareCarriageReturn pins that a bare carriage return (a CR not
// immediately followed by LF) is rejected as a syntax error rather than
// hanging the tokenizer. A lone CR is invalid per the TOML spec, which only
// permits CR as part of a CRLF newline.
func TestDecoderBareCarriageReturn(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input   string
		wantMsg string
		wantCol int
	}{
		"error: lone CR after a value": {
			// The original hang seed: a value line terminated by a bare CR.
			input:   "0=0E0\r0",
			wantMsg: "bare carriage return",
			wantCol: 6,
		},
		"error: lone CR at start of input": {
			input:   "\r",
			wantMsg: "bare carriage return",
			wantCol: 1,
		},
		"error: lone CR before a key": {
			input:   "a=1\rb=2\n",
			wantMsg: "bare carriage return",
			wantCol: 4,
		},
		"error: lone CR followed by another CR": {
			input:   "a=1\r\rb=2\n",
			wantMsg: "bare carriage return",
			wantCol: 4,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			done := make(chan error, 1)
			go func() {
				var out map[string]any
				done <- Unmarshal([]byte(tt.input), &out)
			}()

			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("Unmarshal(%q) = nil error, want %q", tt.input, tt.wantMsg)
				}
				var se *SyntaxError
				if !errors.As(err, &se) {
					t.Fatalf("Unmarshal(%q) error = %T (%v), want *SyntaxError", tt.input, err, err)
				}
				if se.Msg != tt.wantMsg {
					t.Errorf("Unmarshal(%q) Msg = %q, want %q", tt.input, se.Msg, tt.wantMsg)
				}
				if se.Col != tt.wantCol {
					t.Errorf("Unmarshal(%q) Col = %d, want %d", tt.input, se.Col, tt.wantCol)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("Unmarshal(%q) did not return within 5s: tokenizer hang on bare CR", tt.input)
			}
		})
	}
}

// TestDecoderCRLFStillValid guards that fixing the bare-CR case does not break
// legitimate CRLF newlines, which must continue to decode successfully.
func TestDecoderCRLFStillValid(t *testing.T) {
	t.Parallel()

	var out map[string]any
	if err := Unmarshal([]byte("a=1\r\nb=2\r\n"), &out); err != nil {
		t.Fatalf("Unmarshal CRLF doc = %v, want nil", err)
	}
	if got := out["a"]; got != int64(1) {
		t.Errorf("out[a] = %v (%T), want int64(1)", got, got)
	}
	if got := out["b"]; got != int64(2) {
		t.Errorf("out[b] = %v (%T), want int64(2)", got, got)
	}
}
