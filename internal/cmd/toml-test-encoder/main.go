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

// Command toml-test-encoder is a minimal encoder program used to compare this library with other TOML implementations.
package main

import (
	"flag"
	"log"
	"os"
	"path"

	"github.com/zchee/go-toml/internal/cmd/internal/testsuite"
)

func usage() {
	log.Printf("Usage: %s < json-file\n", path.Base(os.Args[0])) //nolint:gosec // G706: program basename in a static usage banner, not attacker-controlled.
	flag.PrintDefaults()
	os.Exit(1)
}

func main() {
	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
	}

	if err := testsuite.EncodeStdin(); err != nil {
		log.Fatal(err)
	}
}
