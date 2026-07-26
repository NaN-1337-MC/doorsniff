package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSeedLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		host    string
		port    uint16
		wantErr bool
	}{
		{name: "host only", line: "mc.hypixel.net", host: "mc.hypixel.net", port: 25565},
		{name: "ipv4", line: "192.168.1.10", host: "192.168.1.10", port: 25565},
		{name: "with port", line: "192.168.1.10:25575", host: "192.168.1.10", port: 25575},
		{name: "with max port", line: "host:65535", host: "host", port: 65535},
		{name: "whitespace", line: "  mc.example.com  ", host: "mc.example.com", port: 25565},
		{name: "invalid port", line: "host:abc", wantErr: true},
		{name: "port 0", line: "host:0", wantErr: true},
		{name: "port too high", line: "host:99999", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := parseSeedLine(tt.line)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("err = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if host != tt.host {
				t.Errorf("host = %q, want %q", host, tt.host)
			}
			if port != tt.port {
				t.Errorf("port = %d, want %d", port, tt.port)
			}
		})
	}
}

func TestParseSeedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := `# comment line
mc.hypixel.net

192.168.1.10:25575
   2b2t.org
mc.example.com:25565  # trailing comment
`
	if err := writeFile(path, content); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := parseSeedFile(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4 (got %+v)", len(got), got)
	}
	wantHosts := []string{"mc.hypixel.net", "192.168.1.10", "2b2t.org", "mc.example.com"}
	wantPorts := []uint16{25565, 25575, 25565, 25565}
	for i, s := range got {
		if s.Host != wantHosts[i] {
			t.Errorf("[%d] host = %q, want %q", i, s.Host, wantHosts[i])
		}
		if s.Port != wantPorts[i] {
			t.Errorf("[%d] port = %d, want %d", i, s.Port, wantPorts[i])
		}
	}
}

func TestLoadSeeds(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "a.txt"), "1.1.1.1\n2.2.2.2\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dir, "b.txt"), "3.3.3.3:25575\n"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSeeds(dir)
	if err != nil {
		t.Fatalf("LoadSeeds: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestLoadSeedsMissingDir(t *testing.T) {
	_, err := LoadSeeds("/nonexistent/path/that/should/not/exist")
	if err == nil {
		t.Errorf("err = nil, want error for missing dir")
	}
}

// writeFile is a tiny helper to keep test bodies terse.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
