package app

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Azaxek/focus-blocker/internal/clock"
	"github.com/Azaxek/focus-blocker/internal/server"
	"github.com/Azaxek/focus-blocker/internal/store"
)

// If another program holds the dashboard port, the service must keep running and
// keep trying: a service that exits because of it blocks nothing.
func TestDashboardKeepsTryingWhenItsPortIsTaken(t *testing.T) {
	squatter, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := squatter.Addr().(*net.TCPAddr).Port
	oldPort, oldRetry := server.Port, retryEvery
	server.Port, retryEvery = port, 40*time.Millisecond
	defer func() { server.Port, retryEvery = oldPort, oldRetry }()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { serveDashboard(ctx, server.New("test"), st, clock.New()); close(done) }()

	time.Sleep(250 * time.Millisecond) // several failed attempts
	select {
	case <-done:
		t.Fatal("gave up while the port was taken")
	default:
	}
	if l := st.ReadLog(10, "error"); len(l) != 1 {
		t.Fatalf("the failure should be logged once, not on every retry: %v", l)
	}

	squatter.Close() // the port frees up
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/api/ping"
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the dashboard never came up: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("did not stop when asked")
	}
}
