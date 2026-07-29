package cli

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExpandVersionToSQLPatterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "exact version", in: "26.1.2", want: []string{"26.1.2"}},
		{name: "wildcard x", in: "1.20.x", want: []string{"1.20."}},
		{name: "major minor only", in: "1.20", want: []string{"1.20."}},
		{name: "range x-y", in: "1.20.x-1.21.x", want: []string{"1.20.", "1.21."}},
		{name: "range major.minor-only", in: "1.20-1.21", want: []string{"1.20.", "1.21."}},
		{name: "comma list", in: "26.1.2,1.20.4", want: []string{"26.1.2", "1.20.4"}},
		{name: "comma + range", in: "1.20.x-1.21.x,26.1.2", want: []string{"1.20.", "1.21.", "26.1.2"}},
		{name: "whitespace trimmed", in: " 1.20.x , 1.21.x ", want: []string{"1.20.", "1.21."}},
		{name: "empty entries skipped", in: "1.20,,26.1.2", want: []string{"1.20.", "26.1.2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExpandVersionToSQLPatterns(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ExpandVersionToSQLPatterns(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

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
