package apps

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var steam = Presets[0]

func TestPresetsAreValid(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Presets {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", r.ID, err)
		}
		if seen[r.ID] {
			t.Errorf("duplicate id %s", r.ID)
		}
		seen[r.ID] = true
	}
	for _, id := range DefaultGlobal {
		if !seen[id] {
			t.Errorf("default %s missing from presets", id)
		}
	}
}

func TestMatchByNameCaseInsensitive(t *testing.T) {
	if !steam.Match(Proc{Name: "Steam.EXE"}) {
		t.Fatal("name match failed")
	}
	if steam.Match(Proc{Name: "notepad.exe", Path: `C:\Windows\notepad.exe`}) {
		t.Fatal("false positive")
	}
}

func TestRenamedExeStillMatches(t *testing.T) {
	// 1. renamed but still inside the Steam folder
	if !steam.Match(Proc{Name: "homework.exe", Path: `D:\Games\Steam\steamapps\common\x\homework.exe`}) {
		t.Fatal("path fragment should catch a rename in place")
	}
	// 2. copied elsewhere and renamed: PE OriginalFilename still says steam.exe
	if !steam.Match(Proc{Name: "notes.exe", Path: `C:\Users\me\Downloads\notes.exe`, Original: "Steam.exe"}) {
		t.Fatal("original filename should catch a copy+rename")
	}
	// 3. only the signature is left
	if !steam.Match(Proc{Name: "x.exe", Path: `C:\tmp\x.exe`, Publisher: "Valve Corporation / Valve"}) {
		t.Fatal("signer should catch it")
	}
}

func TestStorePackageMatch(t *testing.T) {
	mc := Presets[3]
	if !mc.Match(Proc{Name: "Minecraft.Windows.exe", Path: `C:\Program Files\WindowsApps\Microsoft.MinecraftUWP_1.21_x64__8wekyb3d8bbwe\x.exe`}) {
		t.Fatal("store package should match")
	}
}

func TestValidateRejectsDangerousRules(t *testing.T) {
	bad := []Rule{
		{ID: "a", Name: "x", Exe: []string{"explorer.exe"}},
		{ID: "a", Name: "x", Exe: []string{`C:\evil.exe`}},
		{ID: "a", Name: "x", Exe: []string{"notanexe"}},
		{ID: "a", Name: "x", Path: []string{`\a\`}},
		{ID: "A B", Name: "x", Exe: []string{"a.exe"}},
		{ID: "a", Name: "x"},
	}
	for i, r := range bad {
		if r.Validate() == nil {
			t.Errorf("case %d should be rejected", i)
		}
	}
}

type fakeSys struct {
	procs  []Proc
	killed []uint32
	info   map[string][2]string
}

func (f *fakeSys) Procs() ([]Proc, error) { return f.procs, nil }
func (f *fakeSys) Kill(pid uint32) error  { f.killed = append(f.killed, pid); return nil }
func (f *fakeSys) Info(path string) (string, string) {
	i := f.info[path]
	return i[0], i[1]
}

func TestEnforcerKillsMatchesAndChildrenOnly(t *testing.T) {
	dir := t.TempDir()
	renamed := filepath.Join(dir, "notes.exe")
	os.WriteFile(renamed, []byte("x"), 0o644)
	sys := &fakeSys{
		procs: []Proc{
			{PID: 10, PPID: 1, Name: "steam.exe"},
			{PID: 11, PPID: 10, Name: "game.exe", Path: `C:\g\game.exe`},               // child of Steam
			{PID: 12, PPID: 1, Name: "notepad.exe", Path: `C:\Windows\notepad.exe`},    // unrelated
			{PID: 13, PPID: 1, Name: "notes.exe", Path: renamed},                       // renamed Steam copy
			{PID: 14, PPID: 10, Name: "explorer.exe", Path: `C:\Windows\explorer.exe`}, // protected, never killed
		},
		info: map[string][2]string{renamed: {"steam.exe", ""}},
	}
	e := NewEnforcer(sys, 999)
	var notified []Killed
	e.OnKill = func(k Killed) { notified = append(notified, k) }

	if len(e.Scan()) != 0 {
		t.Fatal("nothing should die with no active rules")
	}
	e.SetRules([]Rule{steam})
	got := e.Scan()
	want := map[uint32]bool{10: true, 11: true, 13: true}
	if len(got) != 3 || len(notified) != 3 {
		t.Fatalf("killed %v", got)
	}
	for _, k := range got {
		if !want[k.PID] {
			t.Errorf("killed unexpected %v", k)
		}
	}
}

func TestEnforcerNeverKillsItself(t *testing.T) {
	sys := &fakeSys{procs: []Proc{{PID: 999, Name: "steam.exe"}}}
	e := NewEnforcer(sys, 999)
	e.SetRules([]Rule{steam})
	if len(e.Scan()) != 0 {
		t.Fatal("killed own pid")
	}
}

func TestSignerFromPKCS7(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		Subject:     pkix.Name{CommonName: "Valve Corp.", Organization: []string{"Valve"}},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	// minimal ContentInfo{signedData, [0]{ version, digests{}, content{}, [0] certs }}
	certs := asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: der}
	sd, _ := asn1.Marshal(struct {
		Version int
		Digests asn1.RawValue
		Content asn1.RawValue
		Certs   asn1.RawValue
	}{1, asn1.RawValue{Tag: asn1.TagSet, IsCompound: true}, asn1.RawValue{Tag: asn1.TagSequence, IsCompound: true}, certs})
	blob, _ := asn1.Marshal(struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue
	}{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: sd}})
	if got := SignerFromPKCS7(blob); got != "Valve Corp. / Valve" {
		t.Fatalf("got %q", got)
	}
	if SignerFromPKCS7([]byte("garbage")) != "" {
		t.Fatal("garbage must give empty signer")
	}
}
