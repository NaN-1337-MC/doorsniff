package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PlayerFilter represents a parsed player count predicate.
// Multiple clauses are OR'd together.
type PlayerFilter struct {
	Clauses []PlayerClause
}

// PlayerClause is one condition like ">1" or "5-10".
type PlayerClause struct {
	Op    string // "=", ">", "<", ">=", "<=", "range"
	Lo    int    // used for range and exact
	Hi    int    // used for range
	Raw   string // original text
}

// ParsePlayerFilter parses a player filter string.
// Supported syntax:
//
//	"1-10"     → range 1 to 10
//	">1"       → greater than 1
//	"<10"      → less than 10
//	"=5"       → exactly 5
//	"5"        → exactly 5 (shorthand)
//	"1-10,>50" → OR: range 1-10 or greater than 50
func ParsePlayerFilter(s string) (PlayerFilter, error) {
	if s == "" {
		return PlayerFilter{}, nil
	}
	f := PlayerFilter{}
	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		clause, err := parsePlayerClause(part)
		if err != nil {
			return PlayerFilter{}, err
		}
		f.Clauses = append(f.Clauses, clause)
	}
	return f, nil
}

func parsePlayerClause(s string) (PlayerClause, error) {
	c := PlayerClause{Raw: s}

	// Range: "1-10"
	if idx := strings.Index(s, "-"); idx > 0 {
		// Make sure it's not a negative number like "-5"
		lo, err := strconv.Atoi(s[:idx])
		if err != nil {
			return c, fmt.Errorf("invalid player filter %q: bad range lower bound", s)
		}
		hi, err := strconv.Atoi(s[idx+1:])
		if err != nil {
			return c, fmt.Errorf("invalid player filter %q: bad range upper bound", s)
		}
		if lo > hi {
			return c, fmt.Errorf("invalid player filter %q: lower bound %d > upper bound %d", s, lo, hi)
		}
		c.Op = "range"
		c.Lo = lo
		c.Hi = hi
		return c, nil
	}

	// Operators: >=, <=, >, <, =
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(s, op) {
			val := strings.TrimPrefix(s, op)
			n, err := strconv.Atoi(val)
			if err != nil {
				return c, fmt.Errorf("invalid player filter %q: %q is not a number", s, val)
			}
			c.Op = op
			c.Lo = n
			return c, nil
		}
	}

	// Plain number: "5" → exact match
	n, err := strconv.Atoi(s)
	if err != nil {
		return c, fmt.Errorf("invalid player filter %q: unknown operator (valid: =, >, <, >=, <=, -)", s)
	}
	c.Op = "="
	c.Lo = n
	return c, nil
}

// ToSQL returns a SQL fragment and args for the WHERE clause
// targeting the num_players column.
func (f PlayerFilter) ToSQL() (string, []any) {
	return f.ToSQLColumn("num_players")
}

// ToSQLColumn returns a SQL fragment and args for the WHERE clause
// targeting the given column name.
func (f PlayerFilter) ToSQLColumn(column string) (string, []any) {
	if len(f.Clauses) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for _, c := range f.Clauses {
		switch c.Op {
		case "range":
			parts = append(parts, fmt.Sprintf("(%s BETWEEN ? AND ?)", column))
			args = append(args, c.Lo, c.Hi)
		case "=":
			parts = append(parts, fmt.Sprintf("%s = ?", column))
			args = append(args, c.Lo)
		case ">":
			parts = append(parts, fmt.Sprintf("%s > ?", column))
			args = append(args, c.Lo)
		case "<":
			parts = append(parts, fmt.Sprintf("%s < ?", column))
			args = append(args, c.Lo)
		case ">=":
			parts = append(parts, fmt.Sprintf("%s >= ?", column))
			args = append(args, c.Lo)
		case "<=":
			parts = append(parts, fmt.Sprintf("%s <= ?", column))
			args = append(args, c.Lo)
		}
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// ParseVersionList parses a comma-separated version list.
// Each element is matched as a substring against version_name.
func ParseVersionList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ToVersionSQL returns a SQL fragment and args for version matching.
func ToVersionSQL(versions []string) (string, []any) {
	if len(versions) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for _, v := range versions {
		parts = append(parts, "LOWER(version_name) LIKE ?")
		args = append(args, "%"+strings.ToLower(v)+"%")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// ParsePortFilter parses a port filter: "25565" or "25500-25600".
func ParsePortFilter(s string) (uint16, uint16, error) {
	if s == "" {
		return 0, 0, nil
	}
	if idx := strings.Index(s, "-"); idx > 0 {
		lo, err := strconv.Atoi(s[:idx])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid port filter %q: bad lower bound", s)
		}
		hi, err := strconv.Atoi(s[idx+1:])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid port filter %q: bad upper bound", s)
		}
		if lo < 1 || lo > 65535 || hi < 1 || hi > 65535 {
			return 0, 0, fmt.Errorf("invalid port filter %q: ports must be 1-65535", s)
		}
		return uint16(lo), uint16(hi), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port filter %q: not a number", s)
	}
	if n < 1 || n > 65535 {
		return 0, 0, fmt.Errorf("invalid port filter %q: port must be 1-65535", s)
	}
	return uint16(n), uint16(n), nil
}

// ParseSortKey validates and normalizes a sort key.
func ParseSortKey(s string) (string, error) {
	switch s {
	case "", "recent":
		return "last_seen DESC", nil
	case "players":
		return "num_players DESC", nil
	case "latency":
		return "last_latency_ms ASC", nil
	case "version":
		return "version_name ASC", nil
	case "port":
		return "port ASC", nil
	case "oldest":
		return "first_seen ASC", nil
	case "login":
		return "login_mode ASC", nil
	default:
		return "", fmt.Errorf("invalid sort key %q (valid: players, latency, version, port, recent, oldest, login)", s)
	}
}

// ParseSinceFilter parses a time filter string.
// Supported formats:
//   - Duration: "1h", "30m", "48h" (relative to now, use hours for days)
//   - Unix timestamp: "1700000000"
//   - RFC3339/ISO date: "2024-01-15", "2024-01-15T15:00:00Z"
func ParseSinceFilter(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}

	// Try duration format (1h, 30m, 48h, etc.)
	// Note: Go ParseDuration doesn't support "d" directly, so we convert
	if strings.HasSuffix(s, "h") || strings.HasSuffix(s, "m") {
		d, err := time.ParseDuration(s)
		if err == nil {
			t := time.Now().Add(-d)
			return &t, nil
		}
	}

	// Handle "d" suffix (days) by converting to hours
	if strings.HasSuffix(s, "d") {
		daysStr := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(daysStr)
		if err == nil {
			d := time.Duration(days*24) * time.Hour
			t := time.Now().Add(-d)
			return &t, nil
		}
	}

	// Try Unix timestamp
	if strings.TrimSpace(s) == strings.TrimLeft(strings.TrimSpace(s), "0123456789") && s != "" {
		// All digits - try as unix timestamp
		unix, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			t := time.Unix(unix, 0)
			return &t, nil
		}
	}

	// Try RFC3339 / ISO date formats
	for _, format := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	} {
		t, err := time.Parse(format, s)
		if err == nil {
			return &t, nil
		}
	}

	return nil, fmt.Errorf("invalid --since format %q (valid: 1h, 2d, unix timestamp, or ISO date)", s)
}

// ParsePlayerNames parses a comma-separated list of player name substrings.
func ParsePlayerNames(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParsePluginNames parses a comma-separated list of plugin name substrings.
func ParsePluginNames(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseProtocol parses a protocol filter: "765" or "760-780".
// Returns nil if no filter.
// Note: range syntax (e.g., "760-780") is accepted but only lower bound is used for now.
func ParseProtocol(s string) (*int, error) {
	if s == "" {
		return nil, nil
	}
	if idx := strings.Index(s, "-"); idx > 0 {
		lo, err := strconv.Atoi(s[:idx])
		if err != nil {
			return nil, fmt.Errorf("invalid protocol filter %q: bad lower bound", s)
		}
		_, err = strconv.Atoi(s[idx+1:])
		if err != nil {
			return nil, fmt.Errorf("invalid protocol filter %q: bad upper bound", s)
		}
		return &lo, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("invalid protocol filter %q: not a number", s)
	}
	return &n, nil
}
