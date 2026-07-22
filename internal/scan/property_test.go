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

import (
	"bufio"
	rand "math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"
)

// property_test.go runs PRNG-deterministic property tests asserting that
// the dispatched scan output equals the naive oracle output for 100K
// randomly-generated inputs per scan (length 0..propertyMaxLen, every
// byte value 0..255). On SWAR-only builds the comparison is SWAR vs
// naive; on amd64+goexperiment.simd it is AVX2/SSE2 vs naive; on arm64
// it is NEON vs naive. AC-SIMD-6 is the binding contract.
//
// The seed is read from testdata/property_seed.txt; bumping it is
// intentional and requires a dedicated commit so failures are
// reproducible across CI runs.

const (
	propertyCases    = 100_000
	propertyMaxLen   = 8192
	propertySeedPath = "testdata/property_seed.txt"
)

// loadPropertySeed reads the int64 seed from testdata/property_seed.txt.
// Lines starting with '#' are comments; the first non-comment,
// non-blank line is parsed as a decimal int64.
func loadPropertySeed(tb testing.TB) uint64 {
	tb.Helper()
	f, err := os.Open(propertySeedPath)
	if err != nil {
		tb.Fatalf("open %s: %v", propertySeedPath, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			tb.Fatalf("close %s: %v", propertySeedPath, err)
		}
	}()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		v, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			tb.Fatalf("parse seed %q: %v", line, err)
		}
		return uint64(v)
	}
	if err := sc.Err(); err != nil {
		tb.Fatalf("scan %s: %v", propertySeedPath, err)
	}
	tb.Fatalf("no seed value in %s", propertySeedPath)
	return 0
}

// newPropertyRand returns a deterministic *rand.Rand for a given test
// label. Each scan's property test gets its own stream so test
// reordering does not perturb the input sequence of any one scan.
func newPropertyRand(seed uint64, label string) *rand.Rand {
	var labelHash uint64 = 0xcbf29ce484222325
	for _, c := range []byte(label) {
		labelHash ^= uint64(c)
		labelHash *= 0x100000001b3
	}
	return rand.New(rand.NewPCG(seed, labelHash))
}

// runProperty is the shared body of every TestProperty_* test. It
// runs propertyCases iterations, each with a random length in
// [0, propertyMaxLen] and random byte values, and asserts that
// got(s[:n]) == want(s[:n]).
func runProperty(t *testing.T, label string, got, want func([]byte) int) {
	t.Helper()
	seed := loadPropertySeed(t)
	r := newPropertyRand(seed, label)
	buf := make([]byte, propertyMaxLen)
	for n := range propertyCases {
		l := r.IntN(propertyMaxLen + 1)
		// Fill buf[:l] with random bytes. We fill the buffer 8 bytes at
		// a time via rand.Uint64 to keep PRNG overhead down.
		for i := 0; i < l; {
			w := r.Uint64()
			for k := 0; k < 8 && i < l; k, i = k+1, i+1 {
				buf[i] = byte(w >> (8 * k))
			}
		}
		gotV := got(buf[:l])
		wantV := want(buf[:l])
		if gotV != wantV {
			t.Fatalf("%s case %d (len=%d): got=%d want=%d input=%x",
				label, n, l, gotV, wantV, buf[:l])
		}
	}
}

func TestProperty_ScanBareKey(t *testing.T) {
	t.Parallel()
	runProperty(t, "ScanBareKey", ScanBareKey, naiveScanBareKey)
}

func TestProperty_ScanBasicString(t *testing.T) {
	t.Parallel()
	runProperty(t, "ScanBasicString", ScanBasicString, naiveScanBasicString)
}

func TestProperty_ScanBasicStringEscape(t *testing.T) {
	t.Parallel()
	runProperty(t, "ScanBasicStringEscape", ScanBasicStringEscape, naiveScanBasicStringEscape)
}

func TestProperty_ScanBasicStringStrict(t *testing.T) {
	t.Parallel()
	runProperty(t, "ScanBasicStringStrict", ScanBasicStringStrict, naiveScanBasicStringStrict)
}

func TestProperty_ScanCommentBody(t *testing.T) {
	t.Parallel()
	runProperty(t, "ScanCommentBody", ScanCommentBody, naiveScanCommentBody)
}

func TestProperty_ScanBareValueEnd(t *testing.T) {
	t.Parallel()
	runProperty(t, "ScanBareValueEnd", ScanBareValueEnd, naiveScanBareValueEnd)
}

func TestProperty_CountLines(t *testing.T) {
	t.Parallel()
	runProperty(t, "CountLines", CountLines, naiveCountLines)
}

func TestProperty_ScanLiteralString(t *testing.T) {
	t.Parallel()
	runProperty(t, "ScanLiteralString", ScanLiteralString, naiveScanLiteralString)
}

func TestProperty_SkipWhitespace(t *testing.T) {
	t.Parallel()
	runProperty(t, "SkipWhitespace", SkipWhitespace, naiveSkipWhitespace)
}

func TestProperty_LocateNewline(t *testing.T) {
	t.Parallel()
	runProperty(t, "LocateNewline", LocateNewline, naiveLocateNewline)
}

func TestProperty_ValidateUTF8(t *testing.T) {
	t.Parallel()
	runProperty(t, "ValidateUTF8", ValidateUTF8, naiveValidateUTF8)
}
