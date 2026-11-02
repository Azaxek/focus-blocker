package apps

import (
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinSystem is the real Windows implementation of System.
type WinSystem struct {
	mu    sync.Mutex
	paths map[uint32]pathEntry
}

type pathEntry struct{ name, path string }

func (w *WinSystem) Procs() ([]Proc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.paths == nil {
		w.paths = map[uint32]pathEntry{}
	}
	seen := map[uint32]bool{}
	var out []Proc
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		name := windows.UTF16ToString(e.ExeFile[:])
		seen[e.ProcessID] = true
		c, ok := w.paths[e.ProcessID]
		if !ok || c.name != name { // new process, or the pid was reused
			c = pathEntry{name, imagePath(e.ProcessID)}
			w.paths[e.ProcessID] = c
		}
		out = append(out, Proc{PID: e.ProcessID, PPID: e.ParentProcessID, Name: name, Path: c.path})
	}
	for pid := range w.paths {
		if !seen[pid] {
			delete(w.paths, pid)
		}
	}
	return out, nil
}

func imagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func (*WinSystem) Kill(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

func (*WinSystem) Info(path string) (original, publisher string) {
	return originalFilename(path), SignerFromFile(path)
}

func originalFilename(path string) string {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return ""
	}
	buf := make([]byte, size)
	if windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])) != nil {
		return ""
	}
	query := func(sub string) string {
		var p unsafe.Pointer
		var n uint32
		if windows.VerQueryValue(unsafe.Pointer(&buf[0]), sub, unsafe.Pointer(&p), &n) != nil || n == 0 {
			return ""
		}
		return windows.UTF16PtrToString((*uint16)(p))
	}
	var p unsafe.Pointer
	var n uint32
	if windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\VarFileInfo\Translation`, unsafe.Pointer(&p), &n) != nil || n < 4 {
		return ""
	}
	lang := (*[2]uint16)(p)
	sub := `\StringFileInfo\` + hex4(lang[0]) + hex4(lang[1]) + `\OriginalFilename`
	return strings.TrimSpace(query(sub))
}

func hex4(v uint16) string {
	const d = "0123456789abcdef"
	return string([]byte{d[v>>12&15], d[v>>8&15], d[v>>4&15], d[v&15]})
}
