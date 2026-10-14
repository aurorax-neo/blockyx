package querylog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/0xERR0R/blocky/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/0xERR0R/blocky/log"
)

// DBReader queries query log entries from a database backend
// (sqlite, mysql, postgresql, timescale).
type DBReader struct {
	db *gorm.DB
}

// NewDBReader opens a read-only connection to the query log database.
func NewDBReader(ctx context.Context, dbType config.QueryLogType, target string) (*DBReader, error) {
	var dialector gorm.Dialector

	switch dbType {
	case config.QueryLogTypeMysql:
		dialector = mysql.Open(target)
	case config.QueryLogTypePostgresql, config.QueryLogTypeTimescale:
		dialector = postgres.Open(target)
	case config.QueryLogTypeSqlite:
		d, err := newSQLiteDialector(target)
		if err != nil {
			return nil, err
		}

		dialector = d
	default:
		return nil, fmt.Errorf("unsupported database type for query log reading: %s", dbType)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.New(
			log.Log(),
			logger.Config{
				SlowThreshold:             time.Second,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
			},
		),
	})
	if err != nil {
		return nil, fmt.Errorf("can't open query log database: %w", err)
	}

	return &DBReader{db: db}, nil
}

// applyFilter adds WHERE clauses for the given filter.
func (r *DBReader) applyFilter(db *gorm.DB, f *Filter) *gorm.DB {
	if f == nil {
		return db
	}

	if !f.From.IsZero() {
		db = db.Where("request_ts >= ?", f.From)
	}

	if !f.To.IsZero() {
		db = db.Where("request_ts <= ?", f.To)
	}

	if f.Domain != "" {
		db = db.Where("LOWER(question_name) = LOWER(?)", f.Domain)
	}

	if f.Client != "" {
		db = db.Where("LOWER(client_ip) = LOWER(?) OR LOWER(client_name) = LOWER(?)", f.Client, f.Client)
	}

	if f.QuestionType != "" {
		db = db.Where("LOWER(question_type) = LOWER(?)", f.QuestionType)
	}

	if f.ResponseType != "" {
		db = db.Where("LOWER(response_type) = LOWER(?)", f.ResponseType)
	}

	if f.Search != "" {
		like := "%" + strings.ToLower(f.Search) + "%"
		db = db.Where(
			"LOWER(question_name) LIKE ? OR LOWER(client_ip) LIKE ? OR LOWER(client_name) LIKE ? OR LOWER(answer) LIKE ?",
			like, like, like, like,
		)
	}

	return db
}

func toRecord(e *logEntry) *Record {
	return &Record{
		Time:         e.RequestTS,
		ClientIP:     e.ClientIP,
		ClientName:   e.ClientName,
		DurationMs:   e.DurationMs,
		Reason:       e.Reason,
		QuestionName: e.QuestionName,
		Answer:       e.Answer,
		ResponseCode: e.ResponseCode,
		ResponseType: e.ResponseType,
		QuestionType: e.QuestionType,
	}
}

// Rows returns paginated log entries, newest first.
func (r *DBReader) Rows(f *Filter, limit, offset int) ([]*Record, error) {
	var entries []logEntry

	db := r.applyFilter(r.db.Model(&logEntry{}), f).
		Order("request_ts DESC").
		Limit(limit).
		Offset(offset).
		Find(&entries)
	if db.Error != nil {
		return nil, db.Error
	}

	records := make([]*Record, 0, len(entries))
	for i := range entries {
		records = append(records, toRecord(&entries[i]))
	}

	return records, nil
}

// Count returns the total number of entries matching the filter.
func (r *DBReader) Count(f *Filter) (int64, error) {
	var count int64

	db := r.applyFilter(r.db.Model(&logEntry{}), f).Count(&count)

	return count, db.Error
}

// OverTimeBucket is a time-bucketed aggregation.
type OverTimeBucket struct {
	Time    time.Time
	Total   int
	Blocked int
	Cached  int
}

// OverTime returns per-interval aggregates. The interval is used to truncate
// timestamps; supported databases all provide a date_trunc-like function via
// GORM-agnostic SQL below.
func (r *DBReader) OverTime(f *Filter, interval time.Duration) ([]OverTimeBucket, error) {
	// Fetch matching rows and bucket in Go: portable across sqlite/mysql/postgres
	// without dialect-specific date_trunc SQL.
	var entries []logEntry

	db := r.applyFilter(r.db.Model(&logEntry{}), f).
		Select("request_ts", "response_type", "reason").
		Order("request_ts ASC").
		Find(&entries)
	if db.Error != nil {
		return nil, db.Error
	}

	buckets := make(map[string]*OverTimeBucket)

	var order []string

	for i := range entries {
		rec := toRecord(&entries[i])
		key := rec.Time.Truncate(interval).Format(time.RFC3339)

		b, ok := buckets[key]
		if !ok {
			b = &OverTimeBucket{Time: rec.Time.Truncate(interval)}
			buckets[key] = b
			order = append(order, key)
		}

		b.Total++

		if rec.Blocked() {
			b.Blocked++
		}

		if rec.Cached() {
			b.Cached++
		}
	}

	result := make([]OverTimeBucket, 0, len(order))
	for _, k := range order {
		result = append(result, *buckets[k])
	}

	return result, nil
}

// TopEntry is a ranked name with totals.
type TopEntry struct {
	Name    string
	Total   int
	Blocked int
}

// Top returns the most frequent domains or clients.
func (r *DBReader) Top(f *Filter, byClient bool, blockedOnly bool, limit, offset int) ([]TopEntry, int, error) {
	// Aggregate in Go for portability; the result set is bounded by distinct names.
	var entries []logEntry

	db := r.applyFilter(r.db.Model(&logEntry{}), f).
		Select("question_name", "client_ip", "client_name", "response_type", "reason").
		Find(&entries)
	if db.Error != nil {
		return nil, 0, db.Error
	}

	counts := make(map[string]*TopEntry)

	for i := range entries {
		rec := toRecord(&entries[i])

		if blockedOnly && !rec.Blocked() {
			continue
		}

		name := rec.QuestionName
		if byClient {
			name = rec.ClientIP
			if rec.ClientName != "" {
				name = rec.ClientName
			}
		}

		if name == "" {
			continue
		}

		t, ok := counts[name]
		if !ok {
			t = &TopEntry{Name: name}
			counts[name] = t
		}

		t.Total++

		if rec.Blocked() {
			t.Blocked++
		}
	}

	items := make([]TopEntry, 0, len(counts))
	for _, t := range counts {
		items = append(items, *t)
	}

	// Sort by total desc, name asc for stability.
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Total > items[i].Total ||
				(items[j].Total == items[i].Total && items[j].Name < items[i].Name) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	total := len(items)

	if offset > len(items) {
		offset = len(items)
	}

	items = items[offset:]

	if len(items) > limit {
		items = items[:limit]
	}

	return items, total, nil
}

// Suggestion is a search autocomplete entry.
type Suggestion struct {
	Name  string
	Count int
}

// Search returns distinct names matching the query, most frequent first.
func (r *DBReader) Search(f *Filter, byClient bool, q string, limit int) ([]Suggestion, error) {
	sf := *f
	if q != "" {
		sf.Search = q
	}

	var entries []logEntry

	db := r.applyFilter(r.db.Model(&logEntry{}), &sf).
		Select("question_name", "client_ip", "client_name").
		Find(&entries)
	if db.Error != nil {
		return nil, db.Error
	}

	counts := make(map[string]int)

	var order []string

	for i := range entries {
		rec := toRecord(&entries[i])

		name := rec.QuestionName
		if byClient {
			name = rec.ClientIP
			if rec.ClientName != "" {
				name = rec.ClientName
			}
		}

		if name == "" {
			continue
		}

		if _, ok := counts[name]; !ok {
			order = append(order, name)
		}

		counts[name]++
	}

	// Most frequent first.
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if counts[order[j]] > counts[order[i]] ||
				(counts[order[j]] == counts[order[i]] && order[j] < order[i]) {
				order[i], order[j] = order[j], order[i]
			}
		}
	}

	if len(order) > limit {
		order = order[:limit]
	}

	items := make([]Suggestion, 0, len(order))
	for _, name := range order {
		items = append(items, Suggestion{Name: name, Count: counts[name]})
	}

	return items, nil
}

// QueryTypeCount is a per-type aggregate.
type QueryTypeCount struct {
	Type  string
	Count int
}

// QueryTypes returns counts grouped by DNS query type.
func (r *DBReader) QueryTypes(f *Filter) ([]QueryTypeCount, error) {
	var entries []logEntry

	db := r.applyFilter(r.db.Model(&logEntry{}), f).
		Select("question_type").
		Find(&entries)
	if db.Error != nil {
		return nil, db.Error
	}

	counts := make(map[string]int)
	for i := range entries {
		counts[entries[i].QuestionType]++
	}

	items := make([]QueryTypeCount, 0, len(counts))
	for t, c := range counts {
		items = append(items, QueryTypeCount{Type: t, Count: c})
	}

	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Count > items[i].Count {
				items[i], items[j] = items[j], items[i]
			}
		}
	}

	return items, nil
}

// Close releases the database connection.
func (r *DBReader) Close() error {
	sqlDB, err := r.db.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}
