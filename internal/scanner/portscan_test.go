package scanner

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// startRefusingServer grabs a port on 127.0.0.1 and immediately closes
// the listener. The kernel still rejects new connections to that port
// with ECONNREFUSED, which is what we want to test.
func startRefusingServer(t *testing.T) (port uint16, cleanup func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	p := uint16(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	return p, func() {}
}

// startAcceptingServer starts a TCP listener on 127.0.0.1 that accepts
// connections. The caller is expected to close them quickly.
func startAcceptingServer(t *testing.T) (port uint16, cleanup func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	p := uint16(l.Addr().(*net.TCPAddr).Port)
	return p, func() { _ = l.Close() }
}

func TestProbeRefused(t *testing.T) {
	port, cleanup := startRefusingServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	open, err := Probe(ctx, "127.0.0.1", port, 1*time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if open {
		t.Errorf("open = true, want false (port should be refused)")
	}
}

func TestProbeOpen(t *testing.T) {
	port, cleanup := startAcceptingServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	open, err := Probe(ctx, "127.0.0.1", port, 1*time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !open {
		t.Errorf("open = false, want true")
	}
}

func TestProbeRefusedReturnsFalse(t *testing.T) {
	port, cleanup := startRefusingServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	open, err := Probe(ctx, "127.0.0.1", port, 1*time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if open {
		t.Errorf("open = true, want false (port was closed before listener Accept)")
	}
}

func TestIsConnRefused(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "direct ECONNREFUSED", err: syscall.ECONNREFUSED, want: true},
		{name: "wrapped", err: fmt.Errorf("dial: %w", syscall.ECONNREFUSED), want: true},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "unrelated", err: fmt.Errorf("some other error"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isConnRefused(tt.err); got != tt.want {
				t.Errorf("isConnRefused(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestProbeConcurrent verifies the function is safe to call from
// multiple goroutines.
func TestProbeConcurrent(t *testing.T) {
	port, cleanup := startAcceptingServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	const N = 32
	var wg sync.WaitGroup
	var ok int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			open, err := Probe(ctx, "127.0.0.1", port, 1*time.Second)
			if err == nil && open {
				atomic.AddInt64(&ok, 1)
			}
		}()
	}
	wg.Wait()
	if atomic.LoadInt64(&ok) != N {
		t.Errorf("ok = %d, want %d", ok, N)
	}
	_ = strconv.Itoa
}
