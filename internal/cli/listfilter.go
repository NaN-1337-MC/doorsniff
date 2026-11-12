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

// ExpandVersionToSQLPatterns converts a user-provided version filter
// (possibly comma-separated, possibly with range syntax like "1.20.x-1.21.x")
// into a flat slice of substring patterns to be wrapped with "%" by the
// SQL builder. Each pattern is intended to match any version_name that
// contains it.
//
// Mapping rules:
//
//	"26.1.2"      → ["26.1.2"]   exact: any version_name containing "26.1.2"
//	"1.20"        → ["1.20."]    major.minor: any X.Y.Z where X.Y == "1.20"
//	"1.20.x"      → ["1.20."]    wildcard: same as above
//	"1.20.x-1.21.x" → ["1.20.", "1.21."]   range: inclusive lower, exclusive upper
//	"1.20.x,26.1.2" → ["1.20.", "26.1.2"]  comma-separated = OR of patterns
//
// The returned strings are meant to be wrapped with "%" on both sides
// when building the SQL LIKE clause, so "%1.20.%" matches "Paper 1.20.4"
// and "%26.1.2%" matches "Paper 26.1.2".
func ExpandVersionToSQLPatterns(input string) []string {
	if input == "" {
		return nil
	}
	var out []string
	for _, v := range strings.Split(input, ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		// Range: "1.20.x-1.21.x"
		if idx := strings.Index(v, "-"); idx > 0 {
			lo, hi := v[:idx], v[idx+1:]
			lo = strings.TrimSpace(lo)
			hi = strings.TrimSpace(hi)
			out = append(out, normalizeVersionSegment(lo))
			out = append(out, normalizeVersionSegment(hi))
			continue
		}
		out = append(out, normalizeVersionSegment(v))
	}
	return out
}

// normalizeVersionSegment converts a single version segment like
// "1.20.x", "1.20", or "1.20.4" into the substring pattern that will
// match a version_name containing that version. Wildcards ("x") are
// stripped; bare "X.Y" gets a trailing dot so it doesn't match "X.Y.0"
// by accident under the LIKE wrapper.
func normalizeVersionSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// Drop trailing ".x" wildcard.
	if strings.HasSuffix(s, ".x") {
		s = strings.TrimSuffix(s, ".x")
	}
	// Ensure bare "X.Y" gets a trailing dot so "%X.Y.%" doesn't also
	// match "X.Y0.0" if such a string ever appeared.
	if strings.Count(s, ".") == 1 && !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
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
// When reverse is true, the sort direction is inverted.
func ParseSortKey(s string, reverse bool) (string, error) {
	var col, dir string
	switch s {
	case "", "recent":
		col, dir = "last_seen", "DESC"
	case "players":
		col, dir = "num_players", "DESC"
	case "latency":
		col, dir = "last_latency_ms", "ASC"
	case "version":
		col, dir = "version_name", "ASC"
	case "port":
		col, dir = "port", "ASC"
	case "oldest":
		col, dir = "first_seen", "ASC"
	case "login":
		col, dir = "login_mode", "ASC"
	default:
		return "", fmt.Errorf("invalid sort key %q (valid: players, latency, version, port, recent, oldest, login)", s)
	}
	if reverse {
		if dir == "ASC" {
			dir = "DESC"
		} else {
			dir = "ASC"
		}
	}
	return col + " " + dir, nil
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

// ParseDurationFilter parses a strict duration flag value (e.g. "1h", "7d",
// "30m"). Only duration strings are accepted — no timestamps or ISO dates.
// Returns nil when s is empty.
func ParseDurationFilter(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}

	if strings.HasSuffix(s, "h") || strings.HasSuffix(s, "m") || strings.HasSuffix(s, "s") {
		d, err := time.ParseDuration(s)
		if err == nil {
			t := time.Now().Add(-d)
			return &t, nil
		}
	}

	if strings.HasSuffix(s, "d") {
		daysStr := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(daysStr)
		if err == nil {
			d := time.Duration(days*24) * time.Hour
			t := time.Now().Add(-d)
			return &t, nil
		}
	}

	return nil, fmt.Errorf("invalid duration %q (use e.g. 1h, 30m, 7d)", s)
}

// ParseUntilFilter is the same as ParseSinceFilter but for the until/天花板 bound.
func ParseUntilFilter(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}

	if strings.HasSuffix(s, "h") || strings.HasSuffix(s, "m") {
		d, err := time.ParseDuration(s)
		if err == nil {
			t := time.Now().Add(d)
			return &t, nil
		}
	}

	if strings.HasSuffix(s, "d") {
		daysStr := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(daysStr)
		if err == nil {
			d := time.Duration(days*24) * time.Hour
			t := time.Now().Add(d)
			return &t, nil
		}
	}

	if strings.TrimSpace(s) == strings.TrimLeft(strings.TrimSpace(s), "0123456789") && s != "" {
		unix, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			t := time.Unix(unix, 0)
			return &t, nil
		}
	}

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

	return nil, fmt.Errorf("invalid --until format %q (valid: 1h, 2d, unix timestamp, or ISO date)", s)
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

// rearrangeFlagsFirst returns args with flag tokens (and their immediately
// following non-flag values) moved to the front while preserving relative
// order. This lets the Go flag.Parse() see every flag even when the user
// writes a positional argument before the flags. Equality-flag tokens like
// "--player=Notch" are kept together as one item.
//
// Example:
//
//	in:  ["1.2.3.4:25565", "--player", "Notch", "--since=7d"]
//	out: ["--player", "Notch", "--since=7d", "1.2.3.4:25565"]
func rearrangeFlagsFirst(args []string) []string {
	isFlag := func(s string) bool { return strings.HasPrefix(s, "-") }
	hasInlineValue := func(s string) bool { return strings.Contains(s, "=") }

	out := make([]string, 0, len(args))
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !isFlag(a) {
			positional = append(positional, a)
			continue
		}
		out = append(out, a)
		// If it's a flag without "=", take the next token as its value
		// (provided it does not itself look like a flag).
		if !hasInlineValue(a) && i+1 < len(args) && !isFlag(args[i+1]) {
			out = append(out, args[i+1])
			i++
		}
	}
	return append(out, positional...)
}
