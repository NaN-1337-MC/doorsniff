package scanner

import (
	"testing"
)

func TestParsePortRange(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		lo      uint16
		hi      uint16
		wantErr bool
	}{
		{name: "single port", in: "25565", lo: 25565, hi: 25565},
		{name: "range", in: "25500-25600", lo: 25500, hi: 25600},
		{name: "full", in: "1-65535", lo: 1, hi: 65535},
		{name: "with spaces", in: " 25565-25570 ", lo: 25565, hi: 25570},
		{name: "max port", in: "65535-65535", lo: 65535, hi: 65535},
		{name: "min port", in: "1-1", lo: 1, hi: 1},
		{name: "empty", in: "", wantErr: true},
		{name: "zero", in: "0", wantErr: true},
		{name: "lo > hi", in: "100-50", wantErr: true},
		{name: "non-numeric", in: "abc", wantErr: true},
		{name: "multi-range", in: "1-10,20-30", wantErr: true},
		{name: "overflow", in: "1-70000", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lo, hi, err := ParsePortRange(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("err = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if lo != tt.lo {
				t.Errorf("lo = %d, want %d", lo, tt.lo)
			}
			if hi != tt.hi {
				t.Errorf("hi = %d, want %d", hi, tt.hi)
			}
		})
	}
}
