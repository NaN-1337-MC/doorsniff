package cli

import (
	"fmt"
	"strconv"
	"strings"
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
