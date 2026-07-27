// Package scanner orchestrates target generation, port scanning, and
// protocol probing. It is the only writer of work to the protocol
// layer; storage and CLI consume its results.
package scanner

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/renanbray/doorsniff/internal/model"
)

// LoadSeedFile parses a single seed file and returns the seeds it
// contains. The file format is the same as for individual files read
// by LoadSeeds: one "host" or "host:port" per line, "#" comments
// allowed.
func LoadSeedFile(path string) ([]model.Seed, error) {
	return parseSeedFile(path)
}

// LoadSeeds reads every regular file in dir and returns one Seed per
// non-empty, non-comment line. Each line is "host", "host:port", or
// a CIDR like "10.0.0.0/24". Port defaults to 25565. CIDR entries
// are expanded into individual IPs. Lines starting with "#" are
// ignored.
func LoadSeeds(dir string) ([]model.Seed, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading seeds dir %q: %w", dir, err)
	}

	var seeds []model.Seed
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		fileSeeds, err := parseSeedFile(path)
		if err != nil {
			return nil, fmt.Errorf("parsing %q: %w", path, err)
		}
		seeds = append(seeds, fileSeeds...)
	}
	return seeds, nil
}

func parseSeedFile(path string) ([]model.Seed, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var seeds []model.Seed
	sc := bufio.NewScanner(f)
	now := time.Now().UTC()
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Strip inline comments.
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if line == "" {
			continue
		}
		// CIDR notation: expand into individual IPs.
		if strings.Contains(line, "/") {
			ips, err := expandCIDR(line)
			if err != nil {
				return nil, fmt.Errorf("line %q: %w", line, err)
			}
			for _, ip := range ips {
				seeds = append(seeds, model.Seed{
					Host:    ip,
					Port:    25565,
					AddedAt: now,
				})
			}
			continue
		}
		host, port, err := parseSeedLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %q: %w", line, err)
		}
		seeds = append(seeds, model.Seed{
			Host:    host,
			Port:    port,
			AddedAt: now,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scanning %q: %w", path, err)
	}
	return seeds, nil
}

func expandCIDR(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	var ips []string
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
		ips = append(ips, ip.String())
	}
	// Network and broadcast addresses are excluded by net.Contains
	// for the first and last IPs, so we add them back manually.
	// Actually net.Contains includes the network address but excludes
	// the broadcast — let's just include all addresses net.Contains
	// returns, which is the full usable range.
	return ips, nil
}

// inc increments an IPv4 address in place.
func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func parseSeedLine(line string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(line)
	if err != nil {
		// Treat as host with default port.
		return strings.TrimSpace(line), 25565, nil
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	if port == 0 {
		return "", 0, fmt.Errorf("port 0 is reserved")
	}
	return host, uint16(port), nil
}
