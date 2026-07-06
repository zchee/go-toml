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
	"strings"
	"testing"
)

func seedDecoderCorpus(f *testing.F) {
	for _, rel := range decoderCorpusFiles(f) {
		f.Add(mustReadRepoFile(f, rel))
	}
}

func seedDecoderExamples(f *testing.F) {
	seedDecoderCorpus(f)
	f.Add([]byte(""))
	f.Add([]byte("name = \"Alice\"\n"))
	f.Add([]byte("title = 'Toml'\n"))
	f.Add([]byte("active = true\n"))
	f.Add([]byte("[server]\nports = [80, 443]\n"))
	f.Add([]byte("# comment only\n"))
	f.Add([]byte("bad = \"unterminated\n"))
	f.Add([]byte("x = 1_2_3\n"))
	f.Add([]byte(strings.Repeat("a", 64)))
	// Regression: a value line ending in a bare carriage return (CR not part
	// of a CRLF) once spun the tokenizer forever; it must now be a syntax
	// error. See TestDecoderBareCarriageReturn.
	f.Add([]byte("0=0E0\r0"))
}

func FuzzDecoder(f *testing.F) {
	seedDecoderExamples(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		runDecoderParityProperty(t, "FuzzDecoder", data)
	})
}

func FuzzDecoderConstructorParity(f *testing.F) {
	seedDecoderExamples(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		runDecoderParityProperty(t, "FuzzDecoderConstructorParity", data)
	})
}

func FuzzTokenStream(f *testing.F) {
	seedDecoderExamples(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		runDecoderTokenStreamInvariant(t, "FuzzTokenStream", data)
	})
}
