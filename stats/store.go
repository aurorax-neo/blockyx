package stats

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	// Pure-Go SQLite driver (glebarez fork of modernc), the same one the querylog
	// sqlite writer uses — it registers the "sqlite" driver name, so importing
	// modernc.org/sqlite directly as well would panic on duplicate registration.
	_ "github.com/glebarez/go-sqlite"
)

const statsSchema = `
CREATE TABLE IF NOT EXISTS stats_hourly (
	hour_start      TEXT PRIMARY KEY,
	dropped         INTEGER NOT NULL DEFAULT 0,
	errors          INTEGER NOT NULL DEFAULT 0,
	duration_sum_ms INTEGER NOT NULL DEFAULT 0,
	by_response_type TEXT NOT NULL DEFAULT '{}',
	by_query_type    TEXT NOT NULL DEFAULT '{}',
	by_response_code TEXT NOT NULL DEFAULT '{}',
	domains          TEXT NOT NULL DEFAULT '{}',
	blocked_domains  TEXT NOT NULL DEFAULT '{}',
	clients          TEXT NOT NULL DEFAULT '{}'
);`

// Store persists hourly statistics buckets to SQLite so the /api/stats data
// survives restarts. Buckets are the same unit the Collector aggregates in,
// which makes save/load a lossless round trip.
type Store struct {
	db *sql.DB
}

// OpenStore opens (creating parent schema as needed) the SQLite database at path.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("can't open stats database %q: %w", path, err)
	}

	db.SetMaxOpenConns(1)

	if _, err := db.Exec(statsSchema); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("can't create stats schema in %q: %w", path, err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

func encodeMap(m map[string]int) string {
	if len(m) == 0 {
		return "{}"
	}

	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}

	return string(b)
}

func decodeMap(s string) map[string]int {
	m := map[string]int{}
	if s == "" || s == "{}" {
		return m
	}

	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]int{}
	}

	return m
}

// SaveBucket upserts one hourly bucket. It is safe to call repeatedly for the
// same (still open) bucket; the latest state wins.
func (s *Store) SaveBucket(b *bucket) error {
	_, err := s.db.Exec(`
INSERT INTO stats_hourly (
	hour_start, dropped, errors, duration_sum_ms,
	by_response_type, by_query_type, by_response_code,
	domains, blocked_domains, clients
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(hour_start) DO UPDATE SET
	dropped = excluded.dropped,
	errors = excluded.errors,
	duration_sum_ms = excluded.duration_sum_ms,
	by_response_type = excluded.by_response_type,
	by_query_type = excluded.by_query_type,
	by_response_code = excluded.by_response_code,
	domains = excluded.domains,
	blocked_domains = excluded.blocked_domains,
	clients = excluded.clients`,
		b.hourStart.UTC().Format(time.RFC3339),
		b.dropped,
		b.errors,
		b.durationSumMs,
		encodeMap(b.byResponseType),
		encodeMap(b.byQueryType),
		encodeMap(b.byResponseCode),
		encodeMap(b.domains),
		encodeMap(b.blockedDomains),
		encodeMap(b.clients),
	)
	if err != nil {
		return fmt.Errorf("can't save stats bucket %s: %w", b.hourStart.Format(hourLayout), err)
	}

	return nil
}

// LoadSince returns all buckets with hour_start >= cutoff, oldest first.
func (s *Store) LoadSince(cutoff time.Time) ([]*bucket, error) {
	rows, err := s.db.Query(`
SELECT hour_start, dropped, errors, duration_sum_ms,
	by_response_type, by_query_type, by_response_code,
	domains, blocked_domains, clients
FROM stats_hourly
WHERE hour_start >= ?
ORDER BY hour_start ASC`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("can't load stats buckets: %w", err)
	}
	defer rows.Close()

	var buckets []*bucket

	for rows.Next() {
		var (
			hourStartStr                     string
			dropped, errors                  int
			durationSumMs                    int64
			byResponseType, byQueryType      string
			byResponseCode                   string
			domains, blockedDomains, clients string
		)

		if err := rows.Scan(&hourStartStr, &dropped, &errors, &durationSumMs,
			&byResponseType, &byQueryType, &byResponseCode,
			&domains, &blockedDomains, &clients); err != nil {
			return nil, fmt.Errorf("can't scan stats bucket row: %w", err)
		}

		hourStart, err := time.Parse(time.RFC3339, hourStartStr)
		if err != nil {
			return nil, fmt.Errorf("can't parse stats bucket hour %q: %w", hourStartStr, err)
		}

		b := newBucket(hourStart.UTC())
		b.dropped = dropped
		b.errors = errors
		b.durationSumMs = durationSumMs
		b.byResponseType = decodeMap(byResponseType)
		b.byQueryType = decodeMap(byQueryType)
		b.byResponseCode = decodeMap(byResponseCode)
		b.domains = decodeMap(domains)
		b.blockedDomains = decodeMap(blockedDomains)
		b.clients = decodeMap(clients)

		buckets = append(buckets, b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("can't iterate stats bucket rows: %w", err)
	}

	return buckets, nil
}

// DeleteBefore removes buckets with hour_start older than cutoff (retention).
func (s *Store) DeleteBefore(cutoff time.Time) error {
	_, err := s.db.Exec(`DELETE FROM stats_hourly WHERE hour_start < ?`,
		cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("can't delete old stats buckets: %w", err)
	}

	return nil
}
