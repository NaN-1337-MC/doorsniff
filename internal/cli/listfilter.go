package cli

import (
	"fmt"
	"regexp"
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

// versionMatch checks if the user-provided version input matches
// a server-reported version string that may be an exact version
// (e.g. "1.20.4") or a range (e.g. "1.20.x-1.21.x").
//
// Range syntax: "X.y.x-Z.w.x" means any version from X.y.0 (inclusive)
// to Z.w.0 (exclusive). "X.y.x" alone means the same as X.y.0 to (X.y+1).0.
func VersionMatches(serverVersion, userInput string) bool {
	vm := parseServerVersion(serverVersion)
	if vm == nil {
		return false
	}
	return vm.matches(userInput)
}

// versionMatch is a version comparison target.
type versionMatch interface {
	matches(input string) bool
}

// exactVersion matches a single exact version string.
type exactVersion string

func (e exactVersion) matches(input string) bool {
	return string(e) == input
}

// rangeVersion matches any version in [lo, hi).
type rangeVersion struct {
	lo majorMinor
	hi majorMinor
}

func (r rangeVersion) matches(input string) bool {
	mv, ok := parseMajorMinor(input)
	if !ok {
		return false
	}
	return mv.GE(r.lo) && mv.LT(r.hi)
}

// majorMinor represents a major.minor version for ordering.
type majorMinor struct{ major, minor int }

func (m majorMinor) GE(other majorMinor) bool {
	if m.major != other.major {
		return m.major > other.major
	}
	return m.minor >= other.minor
}

func (m majorMinor) LT(other majorMinor) bool {
	if m.major != other.major {
		return m.major < other.major
	}
	return m.minor < other.minor
}

func parseMajorMinor(s string) (majorMinor, bool) {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return majorMinor{}, false
	}
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return majorMinor{}, false
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return majorMinor{}, false
	}
	return majorMinor{major: maj, minor: min}, true
}

// parseServerVersion extracts a version pattern from a raw server
// version_name string such as "Paper 1.20.4" or "1.20.x-1.21.x".
func parseServerVersion(raw string) versionMatch {
	s := strings.TrimSpace(raw)

	// Try to extract version number from the string.
	// Pattern: major.minor.x (possibly followed by -major.minor.x)
	// Examples: "1.20.x-1.21.x", "Paper 1.20.4", "1.20.4"
	//
	// Regex breakdown:
	// - Optional prefix (Paper, Spigot, BungeeCord, etc.): [^\d]*
	// - Major.minor.patch or major.minor.x: (\d+\.\d+(?:\.\d+|\.x))
	// - Optional range suffix: (?:-\d+\.\d+\.x)?
	mainRX := regexp.MustCompile(`[^\d]*(\d+\.\d+(?:\.\d+|\.x))`)
	m := mainRX.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	version := m[1]

	// Check for range pattern: "1.20.x-1.21.x"
	rangeRX := regexp.MustCompile(`(\d+)\.(\d+)\.x-(\d+)\.(\d+)\.x`)
	rm := rangeRX.FindStringSubmatch(version)
	if rm != nil {
		lo := majorMinor{major: toInt(rm[1]), minor: toInt(rm[2])}
		hi := majorMinor{major: toInt(rm[3]), minor: toInt(rm[4])}
		return rangeVersion{lo: lo, hi: hi}
	}

	// Check for single x pattern: "1.20.x"
	singleX := regexp.MustCompile(`(\d+)\.(\d+)\.x$`)
	sxm := singleX.FindStringSubmatch(version)
	if sxm != nil {
		lo := majorMinor{major: toInt(sxm[1]), minor: toInt(sxm[2])}
		hi := majorMinor{major: lo.major, minor: lo.minor + 1}
		// Edge case: 1.20.x -> hi is 1.21.0
		if lo.minor == 19 { // 1.20.x -> 1.21.0
			hi = majorMinor{major: lo.major + 1, minor: 0}
		}
		return rangeVersion{lo: lo, hi: hi}
	}

	// Exact version: "1.20.4"
	return exactVersion(version)
}

func toInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
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
