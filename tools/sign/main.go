// sign writes <file>.sig: the base64 Ed25519 signature of a file (used for SHA256SUMS).
// The base64 private seed is read from the environment variable named by the first argument.
//
//	RELEASE_SIGNING_KEY=... go run ./tools/sign RELEASE_SIGNING_KEY dist/SHA256SUMS
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: sign <ENV_VAR_WITH_KEY> <file>")
		os.Exit(2)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv(os.Args[1])))
	if err != nil || len(seed) != ed25519.SeedSize {
		fmt.Fprintln(os.Stderr, "sign: the signing key is missing or malformed")
		os.Exit(1)
	}
	data, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), data)
	if err := os.WriteFile(os.Args[2]+".sig", []byte(base64.StdEncoding.EncodeToString(sig)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "sign:", err)
		os.Exit(1)
	}
}
