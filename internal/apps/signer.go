package apps

import (
	"crypto/x509"
	"debug/pe"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"io"
	"os"
)

// SignerFromFile returns "CommonName / Organization" of the Authenticode
// signing certificate embedded in a PE file, or "". The chain is NOT verified:
// a forged signature can only cause extra blocking, never an escape, because a
// stripped signature simply falls back to the other matchers.
func SignerFromFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	pf, err := pe.NewFile(f)
	if err != nil {
		return ""
	}
	var dir pe.DataDirectory
	switch oh := pf.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		if len(oh.DataDirectory) > 4 {
			dir = oh.DataDirectory[4]
		}
	case *pe.OptionalHeader32:
		if len(oh.DataDirectory) > 4 {
			dir = oh.DataDirectory[4]
		}
	}
	if dir.VirtualAddress == 0 || dir.Size < 8 || dir.Size > 1<<20 { // security directory holds a FILE offset
		return ""
	}
	buf := make([]byte, dir.Size)
	if _, err := f.ReadAt(buf, int64(dir.VirtualAddress)); err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	n := binary.LittleEndian.Uint32(buf[:4]) // WIN_CERTIFICATE.dwLength
	if n < 8 || int(n) > len(buf) {
		return ""
	}
	return SignerFromPKCS7(buf[8:n])
}

// SignerFromPKCS7 extracts the leaf (non-CA) certificate's names from a PKCS#7 SignedData blob.
func SignerFromPKCS7(der []byte) string {
	var ci struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue `asn1:"explicit,tag:0"`
	}
	if _, err := asn1.Unmarshal(der, &ci); err != nil {
		return ""
	}
	var sd struct {
		Version int
		Digests asn1.RawValue
		Content asn1.RawValue
		Certs   asn1.RawValue
	}
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil || sd.Certs.Class != asn1.ClassContextSpecific || sd.Certs.Tag != 0 {
		return ""
	}
	var fallback string
	for rest := sd.Certs.Bytes; len(rest) > 0; {
		var raw asn1.RawValue
		var err error
		if rest, err = asn1.Unmarshal(rest, &raw); err != nil {
			break
		}
		cert, err := x509.ParseCertificate(raw.FullBytes)
		if err != nil || cert.IsCA {
			continue
		}
		name := cert.Subject.CommonName
		for _, o := range cert.Subject.Organization {
			name += " / " + o
		}
		for _, u := range cert.ExtKeyUsage {
			if u == x509.ExtKeyUsageCodeSigning {
				return name
			}
		}
		if fallback == "" {
			fallback = name
		}
	}
	return fallback
}
