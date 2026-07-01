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
	"testing"
)

const parserBenchCorpusRel = "testdata/corpus/cargo.lock"

func TestDecoder_NewDecoderBytes_AllocsPerRun(t *testing.T) {
	sample := mustReadRepoFile(t, parserBenchCorpusRel)

	constructorAllocs := testing.AllocsPerRun(100, func() {
		_ = NewDecoderBytes(sample)
	})
	if constructorAllocs > 1 {
		t.Fatalf("NewDecoderBytes(%q) allocs/run = %.0f, want <= 1", parserBenchCorpusRel, constructorAllocs)
	}

	const maxTokenStreamAllocs = 16
	tokenStreamAllocs := testing.AllocsPerRun(25, func() {
		dec := NewDecoderBytes(sample)
		for {
			_, err := dec.ReadToken()
			if err == nil {
				continue
			}
			if errors.Is(err, io.EOF) {
				return
			}
			t.Fatalf("token stream parse error = %v", err)
		}
	})
	if tokenStreamAllocs > maxTokenStreamAllocs {
		t.Fatalf("decode + read token stream allocs/run = %.0f, want <= %d", tokenStreamAllocs, maxTokenStreamAllocs)
	}
}

func readAllTokensFromCorpus(sample []byte) error {
	dec := NewDecoderBytes(sample)
	for {
		_, err := dec.ReadToken()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func BenchmarkDecoderTokens_CargoLock(b *testing.B) {
	body := mustReadRepoFile(b, parserBenchCorpusRel)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))

	// warm-up once outside timer to avoid first-iteration overhead.
	if err := readAllTokensFromCorpus(body); err != nil {
		b.Fatalf("warm-up decode failed: %v", err)
	}

	for b.Loop() {
		if err := readAllTokensFromCorpus(body); err != nil {
			b.Fatalf("decode failed: %v", err)
		}
	}
}
