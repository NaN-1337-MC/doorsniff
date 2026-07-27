package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/renanbray/doorsniff/internal/model"
)

// ListOptions constrains and orders ListServers queries.
type ListOptions struct {
	Limit     int    // 0 = default 50
	Offset    int    // 0 = from start
	OrderBy   string // "last_seen" | "players" | "first_seen"; default "last_seen"
	Version   int    // 0 = no filter
	Country   string // "" = no filter
	Port      uint16 // 0 = no filter
	HasMCOnly bool   // when true, only return rows with at least one protocol hit
}

// Stats summarises the database for the TUI dashboard.
type Stats struct {
	Total       int
	ByVersion   []VersionCount
	ByCountry   []CountryCount
	ByPort      []PortCount
}

// VersionCount is a row in Stats.ByVersion.
type VersionCount struct {
	Protocol int
	Name     string
	Count    int
}

// CountryCount is a row in Stats.ByCountry.
type CountryCount struct {
	Country string
	Count   int
}

// PortCount is a row in Stats.ByPort.
type PortCount struct {
	Port  uint16
	Count int
}

// Repository is the only writer and reader of the servers table.
type Repository struct {
	db *DB
}

// NewRepository returns a Repository backed by db.
func NewRepository(db *DB) *Repository {
	return &Repository{db: db}
}

// UpsertServer inserts or updates a single server. On conflict
// (ip, port), every column is refreshed except first_seen.
func (r *Repository) UpsertServer(ctx context.Context, s model.Server) error {
	sampleJSON, err := jsonOrNull(s.SamplePlayers)
	if err != nil {
		return fmt.Errorf("marshalling sample_players: %w", err)
	}
	pluginsJSON, err := jsonOrNull(s.Plugins)
	if err != nil {
		return fmt.Errorf("marshalling plugins: %w", err)
	}

	const q = `
INSERT INTO servers (
  ip, port, first_seen, last_seen, last_latency_ms,
  version_protocol, version_name, motd,
  num_players, max_players, sample_players,
  query_enabled, plugins, map, gametype, country, raw_query, probe_kind,
  login_mode
) VALUES (
  ?, ?, ?, ?, ?,
  ?, ?, ?,
  ?, ?, ?,
  ?, ?, ?, ?, ?, ?, ?, ?
)
ON CONFLICT (ip, port) DO UPDATE SET
  last_seen       = excluded.last_seen,
  last_latency_ms = excluded.last_latency_ms,
  version_protocol= excluded.version_protocol,
  version_name    = excluded.version_name,
  motd            = excluded.motd,
  num_players     = excluded.num_players,
  max_players     = excluded.max_players,
  sample_players  = excluded.sample_players,
  query_enabled   = excluded.query_enabled,
  plugins         = excluded.plugins,
  map             = excluded.map,
  gametype        = excluded.gametype,
  country         = excluded.country,
  raw_query       = excluded.raw_query,
  probe_kind      = excluded.probe_kind,
  login_mode      = excluded.login_mode;
`

	_, err = r.db.conn.ExecContext(ctx, q,
		s.IP,
		s.Port,
		s.FirstSeen,
		s.LastSeen,
		s.LastLatencyMs,
		s.VersionProtocol,
		nullableString(s.VersionName),
		nullableString(s.MOTD),
		s.NumPlayers,
		s.MaxPlayers,
		sampleJSON,
		s.QueryEnabled,
		pluginsJSON,
		nullableString(s.Map),
		nullableString(s.GameType),
		nullableString(s.Country),
		nullableString(s.RawQuery),
		nullableString(s.ProbeKind),
		nullableString(s.LoginMode),
	)
	if err != nil {
		return fmt.Errorf("upserting server %s: %w", s.Address(), err)
	}
	return nil
}

// ListServers returns a slice of servers matching opts, ordered per
// opts.OrderBy.
func (r *Repository) ListServers(ctx context.Context, opts ListOptions) ([]model.Server, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	orderBy := "last_seen DESC"
	switch opts.OrderBy {
	case "players":
		orderBy = "num_players DESC"
	case "first_seen":
		orderBy = "first_seen DESC"
	case "last_seen", "":
		orderBy = "last_seen DESC"
	default:
		orderBy = "last_seen DESC"
	}

	var (
		conds []string
		args  []any
	)
	if opts.Version != 0 {
		conds = append(conds, "version_protocol = ?")
		args = append(args, opts.Version)
	}
	if opts.Country != "" {
		conds = append(conds, "country = ?")
		args = append(args, opts.Country)
	}
	if opts.Port != 0 {
		conds = append(conds, "port = ?")
		args = append(args, opts.Port)
	}
	if opts.HasMCOnly {
		conds = append(conds, "version_name IS NOT NULL")
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	q := fmt.Sprintf(`
SELECT ip, port, first_seen, last_seen, last_latency_ms,
       version_protocol, version_name, motd,
       num_players, max_players, sample_players,
       query_enabled, plugins, map, gametype, country, raw_query, probe_kind,
       login_mode
FROM servers%s
ORDER BY %s
LIMIT ? OFFSET ?;
`, where, orderBy)

	args = append(args, opts.Limit, opts.Offset)

	rows, err := r.db.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying servers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []model.Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning server: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}
	return out, nil
}

// ListQuery is a flexible filter for ListServersFiltered. Each field
// is optional; zero value means "no filter". The CLI builds this from
// flags and the repo turns it into SQL.
type ListQuery struct {
	PlayerSQL     string   // raw SQL fragment for player filter
	PlayerArgs    []any    // args for PlayerSQL
	MaxPlayerSQL  string   // raw SQL fragment for max players filter
	MaxPlayerArgs []any    // args for MaxPlayerSQL
	Versions      []string // version substrings to match (OR'd)
	Engine        string   // engine substring (case-insensitive LIKE)
	MOTD          string   // MOTD substring (case-insensitive LIKE)
	LoginMode     string   // login mode exact match (cracked, premium, whitelist, banned, kicked, full)
	PortLo        uint16   // 0 = no filter
	PortHi        uint16   // 0 = no filter
	OrderBy       string   // SQL ORDER BY fragment, default "last_seen DESC"
	Limit         int      // 0 = default 50
}

// ListServersFiltered executes a flexible query built from ListQuery.
func (r *Repository) ListServersFiltered(ctx context.Context, q ListQuery) ([]model.Server, error) {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.OrderBy == "" {
		q.OrderBy = "last_seen DESC"
	}

	var (
		conds []string
		args  []any
	)

	// Player filter (already built as raw SQL).
	if q.PlayerSQL != "" {
		conds = append(conds, q.PlayerSQL)
		args = append(args, q.PlayerArgs...)
	}

	// Max players filter.
	if q.MaxPlayerSQL != "" {
		conds = append(conds, q.MaxPlayerSQL)
		args = append(args, q.MaxPlayerArgs...)
	}

	// Version substring match.
	if len(q.Versions) > 0 {
		var parts []string
		for _, v := range q.Versions {
			parts = append(parts, "LOWER(version_name) LIKE ?")
			args = append(args, "%"+strings.ToLower(v)+"%")
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
	}

	// Engine substring.
	if q.Engine != "" {
		conds = append(conds, "LOWER(version_name) LIKE ?")
		args = append(args, "%"+strings.ToLower(q.Engine)+"%")
	}

	// MOTD substring.
	if q.MOTD != "" {
		conds = append(conds, "LOWER(motd) LIKE ?")
		args = append(args, "%"+strings.ToLower(q.MOTD)+"%")
	}

	// Login mode filter.
	if q.LoginMode != "" {
		conds = append(conds, "login_mode = ?")
		args = append(args, q.LoginMode)
	}

	// Port filter.
	if q.PortLo != 0 && q.PortHi != 0 {
		if q.PortLo == q.PortHi {
			conds = append(conds, "port = ?")
			args = append(args, q.PortLo)
		} else {
			conds = append(conds, "port BETWEEN ? AND ?")
			args = append(args, q.PortLo, q.PortHi)
		}
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	sql := fmt.Sprintf(`
SELECT ip, port, first_seen, last_seen, last_latency_ms,
       version_protocol, version_name, motd,
       num_players, max_players, sample_players,
       query_enabled, plugins, map, gametype, country, raw_query, probe_kind,
       login_mode
FROM servers%s
ORDER BY %s
LIMIT ?;
`, where, q.OrderBy)

	args = append(args, q.Limit)

	rows, err := r.db.conn.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("querying servers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []model.Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning server: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}
	return out, nil
}

// GetStats returns aggregated counts for the TUI.
func (r *Repository) GetStats(ctx context.Context) (Stats, error) {
	var s Stats

	if err := r.db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers;`).Scan(&s.Total); err != nil {
		return s, fmt.Errorf("counting servers: %w", err)
	}

	rows, err := r.db.conn.QueryContext(ctx, `
SELECT version_protocol, version_name, COUNT(*) AS n
FROM servers
WHERE version_protocol IS NOT NULL
GROUP BY version_protocol, version_name
ORDER BY n DESC
LIMIT 10;
`)
	if err != nil {
		return s, fmt.Errorf("querying versions: %w", err)
	}
	for rows.Next() {
		var v VersionCount
		if err := rows.Scan(&v.Protocol, &v.Name, &v.Count); err != nil {
			_ = rows.Close()
			return s, fmt.Errorf("scanning version: %w", err)
		}
		s.ByVersion = append(s.ByVersion, v)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return s, fmt.Errorf("iterating versions: %w", err)
	}
	_ = rows.Close()

	rows, err = r.db.conn.QueryContext(ctx, `
SELECT country, COUNT(*) AS n
FROM servers
WHERE country IS NOT NULL
GROUP BY country
ORDER BY n DESC
LIMIT 20;
`)
	if err != nil {
		return s, fmt.Errorf("querying countries: %w", err)
	}
	for rows.Next() {
		var c CountryCount
		if err := rows.Scan(&c.Country, &c.Count); err != nil {
			_ = rows.Close()
			return s, fmt.Errorf("scanning country: %w", err)
		}
		s.ByCountry = append(s.ByCountry, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return s, fmt.Errorf("iterating countries: %w", err)
	}
	_ = rows.Close()

	rows, err = r.db.conn.QueryContext(ctx, `
SELECT port, COUNT(*) AS n
FROM servers
WHERE port != 0
GROUP BY port
ORDER BY n DESC
LIMIT 20;
`)
	if err != nil {
		return s, fmt.Errorf("querying ports: %w", err)
	}
	for rows.Next() {
		var p PortCount
		var port int
		if err := rows.Scan(&port, &p.Count); err != nil {
			_ = rows.Close()
			return s, fmt.Errorf("scanning port: %w", err)
		}
		p.Port = uint16(port)
		s.ByPort = append(s.ByPort, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return s, fmt.Errorf("iterating ports: %w", err)
	}
	_ = rows.Close()

	return s, nil
}

// rowScanner abstracts *sql.Row and *sql.Rows so the same scan code
// works for single-row and multi-row queries.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(rs rowScanner) (model.Server, error) {
	var (
		ipRaw      sql.NullString
		portRaw    sql.NullInt64
		s          model.Server
		versionPr  sql.NullInt64
		versionNm  sql.NullString
		motd       sql.NullString
		numPl      sql.NullInt64
		maxPl      sql.NullInt64
		sampleRaw  sql.NullString
		queryEna   sql.NullBool
		pluginsRaw sql.NullString
		mapName    sql.NullString
		gametype   sql.NullString
		country    sql.NullString
		rawQuery   sql.NullString
		probeKind  sql.NullString
		loginMode  sql.NullString
		latency    sql.NullInt64
		firstSeen  time.Time
		lastSeen   time.Time
	)
	if err := rs.Scan(
		&ipRaw,
		&portRaw,
		&firstSeen,
		&lastSeen,
		&latency,
		&versionPr,
		&versionNm,
		&motd,
		&numPl,
		&maxPl,
		&sampleRaw,
		&queryEna,
		&pluginsRaw,
		&mapName,
		&gametype,
		&country,
		&rawQuery,
		&probeKind,
		&loginMode,
	); err != nil {
		return s, err
	}
	if !ipRaw.Valid || ipRaw.String == "" {
		return s, fmt.Errorf("corrupted row: ip is NULL or empty (data integrity violation)")
	}
	if !portRaw.Valid || portRaw.Int64 == 0 {
		return s, fmt.Errorf("corrupted row: port is NULL or 0 (data integrity violation)")
	}
	s.IP = ipRaw.String
	s.Port = uint16(portRaw.Int64)
	s.FirstSeen = firstSeen
	s.LastSeen = lastSeen
	if latency.Valid {
		s.LastLatencyMs = latency.Int64
	}
	if versionPr.Valid {
		s.VersionProtocol = int(versionPr.Int64)
	}
	s.VersionName = versionNm.String
	s.MOTD = motd.String
	if numPl.Valid {
		s.NumPlayers = int(numPl.Int64)
	}
	if maxPl.Valid {
		s.MaxPlayers = int(maxPl.Int64)
	}
	if sampleRaw.Valid && sampleRaw.String != "" {
		_ = json.Unmarshal([]byte(sampleRaw.String), &s.SamplePlayers)
	}
	if queryEna.Valid {
		s.QueryEnabled = queryEna.Bool
	}
	if pluginsRaw.Valid && pluginsRaw.String != "" {
		_ = json.Unmarshal([]byte(pluginsRaw.String), &s.Plugins)
	}
	s.Map = mapName.String
	s.GameType = gametype.String
	s.Country = country.String
	s.RawQuery = rawQuery.String
	s.ProbeKind = probeKind.String
	s.LoginMode = loginMode.String
	return s, nil
}

// jsonOrNull marshals v to JSON or returns "null" for empty slices so
// the database stores JSON `null` rather than an empty document.
func jsonOrNull[T any](v []T) (any, error) {
	if len(v) == 0 {
		return "null", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// nullableString returns nil for empty strings so the column stores NULL.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// LoadProgress returns the set of seeds whose scan was completed,
// keyed by seed IP with the last port that was scanned.
func (r *Repository) LoadProgress(ctx context.Context) (map[string]uint16, error) {
	rows, err := r.db.conn.QueryContext(ctx, `SELECT seed_ip, last_port FROM scan_progress;`)
	if err != nil {
		return nil, fmt.Errorf("querying scan_progress: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]uint16)
	for rows.Next() {
		var ip string
		var port uint16
		if err := rows.Scan(&ip, &port); err != nil {
			return nil, fmt.Errorf("scanning scan_progress row: %w", err)
		}
		out[ip] = port
	}
	return out, rows.Err()
}

// SaveProgress records that seedIP was fully scanned up to lastPort.
func (r *Repository) SaveProgress(ctx context.Context, seedIP string, lastPort uint16) error {
	const q = `
INSERT INTO scan_progress (seed_ip, last_port) VALUES (?, ?)
ON CONFLICT (seed_ip) DO UPDATE SET
  last_port  = excluded.last_port,
  updated_at = CURRENT_TIMESTAMP;
`
	if _, err := r.db.conn.ExecContext(ctx, q, seedIP, lastPort); err != nil {
		return fmt.Errorf("saving progress for %s: %w", seedIP, err)
	}
	return nil
}

// UpdateLoginMode updates only the login_mode column for an existing server.
func (r *Repository) UpdateLoginMode(ctx context.Context, ip string, port uint16, mode string) error {
	const q = `
UPDATE servers SET login_mode = ? WHERE ip = ? AND port = ?;
`
	if _, err := r.db.conn.ExecContext(ctx, q, nullableString(mode), ip, port); err != nil {
		return fmt.Errorf("updating login_mode for %s:%d: %w", ip, port, err)
	}
	return nil
}
