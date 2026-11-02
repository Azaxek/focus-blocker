package main

import "testing"

// A build with the placeholder or a malformed key would silently disable
// self-update, so make that a test failure instead.
func TestReleasePublicKeyIsConfigured(t *testing.T) {
	if k := pubKey(); len(k) != 32 {
		t.Fatalf("releasePubKey is not a valid base64 Ed25519 public key (got %d bytes)", len(k))
	}
}
