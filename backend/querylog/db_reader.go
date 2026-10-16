package querylog

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/0xERR0R/blocky/config"
	"github.com/0xERR0R/blocky/log"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DBReader queries query log entries from a database backend
// (sqlite, mysql, postgresql, timescale).
// The mutex stays first so a reader value is never copied with its lock.
type DBReader struct {
	rollupMu        sync.Mutex
	db              *gorm.DB
	dbType          config.QueryLogType
	cancelBackfill  context.CancelFunc
	backfillDone    chan struct{}
	rollupCond      *sync.Cond
	rollupReady     bool
	recentReady     bool
	rollupErr       error
	backfillStopped bool
	coveredCounter  int64
	coveredHour     int64
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
		opened, err := newSQLiteDialector(target)
		if err != nil {
			return nil, err
		}

		dialector = opened
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

	reader := &DBReader{db: db, dbType: dbType}
	reader.backfillDone = make(chan struct{})
	reader.rollupCond = sync.NewCond(&reader.rollupMu)
	reader.ensureIndexes()
	reader.startBackfill(ctx)

	return reader, nil
}

func (r *DBReader) ensureIndexes() {
	tableName := "log_entries"
	switch r.dbType {
	case config.QueryLogTypePostgresql, config.QueryLogTypeTimescale, config.QueryLogTypeSqlite:
		_ = r.db.Exec("CREATE INDEX IF NOT EXISTS idx_log_entries_ts_qname ON " + tableName + " (request_ts, question_name)").Error
		_ = r.db.Exec("CREATE INDEX IF NOT EXISTS idx_log_entries_ts_client ON " + tableName + " (request_ts, client_name, client_ip)").Error
		_ = r.db.Exec("CREATE INDEX IF NOT EXISTS idx_log_entries_ts_resp ON " + tableName + " (request_ts, response_type)").Error
	case config.QueryLogTypeMysql:
		_ = r.db.Exec("CREATE INDEX idx_log_entries_ts_qname ON `" + tableName + "` (request_ts, question_name)").Error
		_ = r.db.Exec("CREATE INDEX idx_log_entries_ts_client ON `" + tableName + "` (request_ts, client_name, client_ip)").Error
		_ = r.db.Exec("CREATE INDEX idx_log_entries_ts_resp ON `" + tableName + "` (request_ts, response_type)").Error
	default:
		return
	}
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

// OverTimeBucket is a time-bucketed aggregation.
type OverTimeBucket struct {
	Time    time.Time
	Total   int
	Blocked int
	Cached  int
}

// TopEntry is a ranked name with totals.
type TopEntry struct {
	Name    string
	Total   int
	Blocked int
}

// Suggestion is a search autocomplete entry.
type Suggestion struct {
	Name  string
	Count int
}

// Search returns distinct names matching the query, most frequent first.
func (r *DBReader) Search(f *Filter, byClient bool, q string, limit int) ([]Suggestion, error) {
	type searchRow struct {
		Name  string `gorm:"column:name"`
		Count int    `gorm:"column:count"`
	}

	nameExpr, filterCond := groupedName(byClient)
	dbQuery := r.applyFilter(r.db.Model(&logEntry{}), f).Where(filterCond)
	if q != "" {
		dbQuery = dbQuery.Where("LOWER("+nameExpr+") LIKE ?", "%"+strings.ToLower(q)+"%")
	}

	var rows []searchRow
	err := dbQuery.Select(nameExpr + " AS name, COUNT(*) AS count").
		Group(nameExpr).
		Order("count DESC, name ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	items := make([]Suggestion, 0, len(rows))
	for _, row := range rows {
		items = append(items, Suggestion{Name: row.Name, Count: row.Count})
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
	type qtypeRow struct {
		Type  string `gorm:"column:type"`
		Count int    `gorm:"column:count"`
	}

	var rows []qtypeRow
	err := r.applyFilter(r.db.Model(&logEntry{}), f).
		Where("question_type != '' AND question_type IS NOT NULL").
		Select("question_type AS type, COUNT(*) AS count").
		Group("question_type").
		Order("count DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	items := make([]QueryTypeCount, 0, len(rows))
	for _, row := range rows {
		items = append(items, QueryTypeCount{Type: row.Type, Count: row.Count})
	}

	return items, nil
}

// Close releases the database connection.
func (r *DBReader) Close() error {
	r.stopBackfill()

	sqlDB, err := r.db.DB()
	if err != nil {
		return err
	}

	return sqlDB.Close()
}
