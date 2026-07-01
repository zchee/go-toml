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

package scan

func basicStringStrictStop(b byte) bool {
	return b == '"' || b == '\\' || b == 0x7f || (b < 0x20 && b != '\t')
}

//nolint:unused // called by scan_amd64.go/scan_swar.go tails; unused only on the arm64 build the linter analyzes.
func scanBasicStringStrictScalar(s []byte) int {
	for i, b := range s {
		if basicStringStrictStop(b) {
			return i
		}
	}
	return len(s)
}

func commentBodyStop(b byte) bool {
	return b == 0x7f || (b < 0x20 && b != '\t')
}

//nolint:unused // called by scan_amd64.go/scan_swar.go tails; unused only on the arm64 build the linter analyzes.
func scanCommentBodyScalar(s []byte) int {
	for i, b := range s {
		if commentBodyStop(b) {
			return i
		}
	}
	return len(s)
}

func bareValueDelimiter(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', ',', ']', '}', '#', '=':
		return true
	default:
		return false
	}
}

//nolint:unused // called by scan_amd64.go/scan_swar.go tails; unused only on the arm64 build the linter analyzes.
func scanBareValueEndScalar(s []byte) int {
	for i, b := range s {
		if bareValueDelimiter(b) {
			return i
		}
	}
	return len(s)
}

//nolint:unused // called by scan_amd64.go/scan_swar.go tails; unused only on the arm64 build the linter analyzes.
func countLinesScalar(s []byte) int {
	n := 0
	for _, b := range s {
		if b == '\n' {
			n++
		}
	}
	return n
}
