package sites

import (
	"net"
	"testing"
	"time"
)

func TestBrowserConnFilter(t *testing.T) {
	ips := map[[4]byte]bool{{104, 26, 14, 191}: true}
	row := tcpRow{remoteAddr: 104 | 26<<8 | 14<<16 | 191<<24, remotePort: 0xbb01, pid: 7} // 0xbb01 is 443 in network order
	exe := map[uint32]string{7: `C:\Program Files\Microsoft\Edge\Application\msedge.exe`, 8: `C:\Games\steam.exe`}
	match := browserConn(ips, func(pid uint32) string { return exe[pid] })

	if !match(row) {
		t.Fatal("an Edge connection to a blocked site's address should be reset")
	}
	other := row
	other.pid = 8
	if match(other) {
		t.Fatal("a connection held by a program that is not a browser must be left alone")
	}
	other = row
	other.remoteAddr = 8 | 8<<8 | 8<<16 | 8<<24
	if match(other) {
		t.Fatal("a connection to an unrelated address must be left alone")
	}
	other = row
	other.remotePort = 0x1600 // port 22
	if match(other) {
		t.Fatal("only web ports are touched")
	}
}

// Resetting needs administrator rights, so this skips in a normal test run and
// proves the real thing when the tests are run from an elevated shell.
func TestDropConnsResetsALiveConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	n := dropConns(func(r tcpRow) bool { return r.remoteIP() == [4]byte{127, 0, 0, 1} && r.remotePortNum() == port })
	if n == 0 {
		t.Skip("could not reset a connection (needs an elevated shell)")
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("the connection still works after it was reset")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatalf("the connection was not reset: %v", err)
	}
}
