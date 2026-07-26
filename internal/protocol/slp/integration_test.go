//go:build integration

package slp

import (
	"context"
	"testing"
	"time"
)

// TestIntegrationPingPublicServer pings a well-known public Minecraft
// server. It is gated behind the "integration" build tag so the
// default `go test ./...` does not depend on the network.
//
// Run with: go test -tags=integration -run Integration ./internal/protocol/slp
func TestIntegrationPingPublicServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, latency, err := Ping(ctx, "mc.hypixel.net", 25565, 3*time.Second)
	if err != nil {
		t.Skipf("integration test skipped: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}
	if resp.Version.Protocol == 0 {
		t.Errorf("protocol = 0, want > 0")
	}
	if resp.Version.Name == "" {
		t.Errorf("version.name is empty")
	}
	if latency <= 0 {
		t.Errorf("latency = %v, want > 0", latency)
	}
	t.Logf("got: version=%s protocol=%d players=%d/%d latency=%v",
		resp.Version.Name, resp.Version.Protocol,
		resp.Players.Online, resp.Players.Max, latency)
}
