package sites

import (
	"sync"
	"time"
	"unsafe"

	"github.com/miekg/dns"
	"golang.org/x/sys/windows"

	"github.com/Azaxek/focus-blocker/internal/apps"
)

// A block only stops new lookups. A browser that already holds an open
// connection to a site's real server keeps sending new requests down it without
// resolving the name again, so the site keeps loading. dropLive resets those
// connections: the browser then has to look the name up and gets the sinkhole.

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	setTcpEntry         = iphlpapi.NewProc("SetTcpEntry")
)

const (
	tcpTableOwnerPIDAll = 5 // TCP_TABLE_OWNER_PID_ALL
	tcpEstablished      = 5
	tcpDeleteTCB        = 12
)

// tcpRow is MIB_TCPROW_OWNER_PID. Addresses are in network order and ports hold
// the network-order 16 bits in the low half.
type tcpRow struct{ state, localAddr, localPort, remoteAddr, remotePort, pid uint32 }

func (r tcpRow) remoteIP() [4]byte {
	return [4]byte{byte(r.remoteAddr), byte(r.remoteAddr >> 8), byte(r.remoteAddr >> 16), byte(r.remoteAddr >> 24)}
}

func (r tcpRow) remotePortNum() uint16 {
	return uint16(r.remotePort&0xff)<<8 | uint16(r.remotePort>>8&0xff)
}

// tcpTable lists the IPv4 TCP connections with their owning process.
// ponytail: IPv4 only, because SetTcpEntry cannot reset IPv6 connections.
func tcpTable() []tcpRow {
	var buf []byte
	size := uint32(0)
	for range 5 { // the table can grow between the size query and the read
		var p uintptr
		if len(buf) > 0 {
			p = uintptr(unsafe.Pointer(&buf[0]))
		}
		r, _, _ := getExtendedTcpTable.Call(p, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, tcpTableOwnerPIDAll, 0)
		switch r {
		case 0:
			n := *(*uint32)(unsafe.Pointer(&buf[0]))
			if n == 0 {
				return nil
			}
			return append([]tcpRow(nil), unsafe.Slice((*tcpRow)(unsafe.Pointer(&buf[4])), n)...)
		case uintptr(windows.ERROR_INSUFFICIENT_BUFFER):
			buf = make([]byte, size+1024)
		default:
			return nil
		}
	}
	return nil
}

// dropConns resets every established connection that match accepts and returns
// how many it reset. Resetting needs administrator rights, which the service has.
func dropConns(match func(tcpRow) bool) (n int) {
	for _, r := range tcpTable() {
		if r.state != tcpEstablished || !match(r) {
			continue
		}
		row := [5]uint32{tcpDeleteTCB, r.localAddr, r.localPort, r.remoteAddr, r.remotePort}
		if ret, _, _ := setTcpEntry.Call(uintptr(unsafe.Pointer(&row[0]))); ret == 0 {
			n++
		}
	}
	return n
}

// realIPs asks public resolvers directly (plain DNS, so neither the hosts file nor
// the sinkhole is involved) where the blocked sites really live.
// ponytail: only each domain and its www name; other subdomains usually share those addresses.
func realIPs(domains []string) map[[4]byte]bool {
	out := map[[4]byte]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, d := range domains {
		for _, name := range []string{d, "www." + d} {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				ips := lookupA(name)
				mu.Lock()
				for _, ip := range ips {
					out[ip] = true
				}
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	return out
}

func lookupA(name string) (ips [][4]byte) {
	m := new(dns.Msg).SetQuestion(dns.Fqdn(name), dns.TypeA)
	c := &dns.Client{Timeout: 2 * time.Second}
	for _, upstream := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
		r, _, err := c.Exchange(m, upstream)
		if err != nil {
			continue
		}
		for _, rr := range r.Answer {
			if a, ok := rr.(*dns.A); ok && a.A.To4() != nil {
				ips = append(ips, [4]byte(a.A.To4()))
			}
		}
		return ips
	}
	return nil
}

// browserConn accepts a web connection (port 80 or 443) held by a browser to one of ips.
func browserConn(ips map[[4]byte]bool, exeOf func(pid uint32) string) func(tcpRow) bool {
	return func(r tcpRow) bool {
		if p := r.remotePortNum(); (p != 443 && p != 80) || !ips[r.remoteIP()] {
			return false
		}
		return apps.IsBrowser(exeOf(r.pid))
	}
}

// liveDrop remembers where the blocked sites live between passes.
type liveDrop struct {
	busy sync.Mutex // one pass at a time; a pass that finds it taken is skipped
	fp   string
	ips  map[[4]byte]bool
}

// dropLive resets the connections browsers opened to the blocked sites before the block began.
func (b *Blocker) dropLive(fp string, domains []string) {
	d := &b.drop
	if !d.busy.TryLock() {
		return
	}
	defer d.busy.Unlock()
	if d.fp != fp || d.ips == nil {
		d.fp, d.ips = fp, realIPs(domains)
	}
	dropConns(browserConn(d.ips, apps.ImagePath))
}
