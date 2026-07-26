package scanner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"
)

// Probe attempts a TCP connect to host:port within timeout. It returns
//
//	(true,  nil)            the port accepted the connection
//	(false, nil)            the connection was refused (server alive, port closed)
//	(false, err)            any other failure (timeout, DNS, etc.)
//
// The caller may treat the (false, nil) case as a normal "port closed"
// outcome and skip subsequent protocol probes.
func Probe(ctx context.Context, host string, port uint16, timeout time.Duration) (bool, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		if isConnRefused(err) {
			return false, nil
		}
		return false, fmt.Errorf("dialing %s: %w", addr, err)
	}
	// Disable Nagle so the small SLP handshake + status-request pair
	// is sent immediately. Without this, the kernel may coalesce the
	// two writes into a single TCP segment and confuse servers that
	// expect one packet per write.
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	_ = conn.Close()
	return true, nil
}

// isConnRefused unwraps the error chain looking for syscall.ECONNREFUSED.
// This is the standard "connection refused" returned when no process
// is listening on the target port.
func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return isConnRefused(opErr.Err)
	}
	var sysErr *os.SyscallError
	if errors.As(err, &sysErr) {
		return errors.Is(sysErr.Err, syscall.ECONNREFUSED)
	}
	return false
}
