package scanner

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsePortRange parses a port-range string. Accepted forms:
//
//	"25565"           -> [25565, 25565]
//	"25500-25600"     -> [25500, 25600]
//	"1-65535"         -> [1, 65535]
//	"a-b,c-d"         -> error (single range only — multi-range lives in higher layers)
//
// Port 0 is rejected; lo must be <= hi.
func ParsePortRange(s string) (uint16, uint16, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("empty port range")
	}
	if strings.Contains(s, ",") {
		return 0, 0, fmt.Errorf("multi-range not supported in single ParsePortRange call: %q", s)
	}

	if idx := strings.Index(s, "-"); idx >= 0 {
		lo, err := strconv.ParseUint(strings.TrimSpace(s[:idx]), 10, 16)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid low port %q: %w", s[:idx], err)
		}
		hi, err := strconv.ParseUint(strings.TrimSpace(s[idx+1:]), 10, 16)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid high port %q: %w", s[idx+1:], err)
		}
		if lo == 0 || hi == 0 {
			return 0, 0, fmt.Errorf("port 0 is reserved")
		}
		if lo > hi {
			return 0, 0, fmt.Errorf("low port %d > high port %d", lo, hi)
		}
		return uint16(lo), uint16(hi), nil
	}

	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port %q: %w", s, err)
	}
	if n == 0 {
		return 0, 0, fmt.Errorf("port 0 is reserved")
	}
	return uint16(n), uint16(n), nil
}
