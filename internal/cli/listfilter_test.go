package cli

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRearrangeFlagsFirst(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "no flags",
			in:   []string{"1.2.3.4:25565"},
			want: []string{"1.2.3.4:25565"},
		},
		{
			name: "flag before positional",
			in:   []string{"--player", "Notch", "1.2.3.4:25565"},
			want: []string{"--player", "Notch", "1.2.3.4:25565"},
		},
		{
			name: "positional before flag (key case)",
			in:   []string{"1.2.3.4:25565", "--player", "Notch"},
			want: []string{"--player", "Notch", "1.2.3.4:25565"},
		},
		{
			name: "multiple flags mixed",
			in:   []string{"1.2.3.4:25565", "--player", "Notch", "--since", "7d"},
			want: []string{"--player", "Notch", "--since", "7d", "1.2.3.4:25565"},
		},
		{
			name: "inline = value",
			in:   []string{"1.2.3.4:25565", "--since=7d", "--player", "Notch"},
			want: []string{"--since=7d", "--player", "Notch", "1.2.3.4:25565"},
		},
		{
			name: "multiple positionals",
			in:   []string{"1.2.3.4:25565", "5.6.7.8", "--player", "Notch"},
			want: []string{"--player", "Notch", "1.2.3.4:25565", "5.6.7.8"},
		},
		{
			name: "empty input",
			in:   []string{},
			want: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rearrangeFlagsFirst(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("rearrangeFlagsFirst(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseDurationFilter(t *testing.T) {
	t.Parallel()
	now := time.Now()

	cases := []struct {
		name      string
		input     string
		wantOK    bool
		wantDelta time.Duration // expected duration between now and result (approx)
	}{
		{name: "empty", input: "", wantOK: true, wantDelta: 0},
		{name: "1 hour", input: "1h", wantOK: true, wantDelta: time.Hour},
		{name: "30 minutes", input: "30m", wantOK: true, wantDelta: 30 * time.Minute},
		{name: "7 days", input: "7d", wantOK: true, wantDelta: 7 * 24 * time.Hour},
		{name: "45 seconds", input: "45s", wantOK: true, wantDelta: 45 * time.Second},
		{name: "invalid (timestamp)", input: "1700000000", wantOK: false},
		{name: "invalid (iso date)", input: "2024-01-15", wantOK: false},
		{name: "invalid (text)", input: "lastweek", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDurationFilter(tc.input)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("ParseDurationFilter(%q) error: %v", tc.input, err)
				}
				if tc.input == "" {
					if got != nil {
						t.Errorf("empty input should return nil, got %v", got)
					}
					return
				}
				if got == nil {
					t.Fatalf("ParseDurationFilter(%q) returned nil", tc.input)
				}
				delta := now.Sub(*got)
				// Allow ±1s slack for slow CI machines.
				diff := delta - tc.wantDelta
				if diff < -time.Second || diff > time.Second {
					t.Errorf("delta = %v, want ~%v", delta, tc.wantDelta)
				}
			} else {
				if err == nil {
					t.Errorf("ParseDurationFilter(%q) expected error, got nil", tc.input)
				}
				if !strings.Contains(err.Error(), "invalid") {
					t.Errorf("error message = %q, want it to mention invalid", err.Error())
				}
			}
		})
	}
}
