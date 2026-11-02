// keygen creates the Ed25519 key pair that signs releases.
//
//	go run ./tools/keygen            # prints the PUBLIC key; the private key goes to stdout only with --private
//	go run ./tools/keygen --private  # prints the private key (pipe it into `gh secret set RELEASE_SIGNING_KEY`)
//
// The public key is committed in cmd/focusguard/main.go; the private key lives
// only in the GitHub Actions secret RELEASE_SIGNING_KEY.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "--pair" {
		// one run, both values: "<private>\n<public>"
		fmt.Println(base64.StdEncoding.EncodeToString(priv.Seed()))
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
		return
	}
	fmt.Fprintln(os.Stderr, "usage: keygen --pair   (prints private seed then public key, base64, one per line)")
	os.Exit(2)
}
