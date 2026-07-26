package model

import (
	"testing"
)

func TestTargetAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		t    Target
		want string
	}{
		{name: "ipv4", t: Target{IP: "192.168.1.1", Port: 25565}, want: "192.168.1.1:25565"},
		{name: "ipv6", t: Target{IP: "::1", Port: 25565}, want: "[::1]:25565"},
		{name: "high port", t: Target{IP: "10.0.0.1", Port: 65535}, want: "10.0.0.1:65535"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.t.Address(); got != tt.want {
				t.Errorf("Address() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServerIsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    Server
		want bool
	}{
		{name: "zero value", s: Server{}, want: true},
		{name: "ip only", s: Server{IP: "1.2.3.4"}, want: false},
		{name: "port only", s: Server{Port: 25565}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.s.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServerAddress(t *testing.T) {
	t.Parallel()

	s := Server{IP: "192.168.1.1", Port: 25565}
	if got, want := s.Address(), "192.168.1.1:25565"; got != want {
		t.Errorf("Address() = %q, want %q", got, want)
	}
}

func TestSeedAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    Seed
		want string
	}{
		{name: "no port defaults 25565", s: Seed{Host: "mc.example.com"}, want: "mc.example.com:25565"},
		{name: "explicit port", s: Seed{Host: "mc.example.com", Port: 25575}, want: "mc.example.com:25575"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.s.Address(); got != tt.want {
				t.Errorf("Address() = %q, want %q", got, tt.want)
			}
		})
	}
}
