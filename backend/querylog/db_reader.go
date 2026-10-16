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
	db     *gorm.DB
	dbType config.QueryLogType
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

	return &DBReader{db: db, dbType: dbType}, nil
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

// OverTime returns per-interval aggregates.
func (r *DBReader) OverTime(f *Filter, interval time.Duration) ([]OverTimeBucket, error) {
	intervalSec := int64(interval.Seconds())
	if intervalSec <= 0 {
		intervalSec = 3600
	}

	var bucketExpr string
	switch r.dbType {
	case config.QueryLogTypeMysql:
		bucketExpr = fmt.Sprintf("(FLOOR(UNIX_TIMESTAMP(request_ts) / %d) * %d)", intervalSec, intervalSec)
	case config.QueryLogTypePostgresql, config.QueryLogTypeTimescale:
		if intervalSec == 3600 {
			bucketExpr = "extract(epoch from date_trunc('hour', request_ts))::bigint"
		} else if intervalSec == 86400 {
			bucketExpr = "extract(epoch from date_trunc('day', request_ts))::bigint"
		} else if intervalSec == 60 {
			bucketExpr = "extract(epoch from date_trunc('minute', request_ts))::bigint"
		} else {
			bucketExpr = fmt.Sprintf("(floor(extract(epoch from request_ts) / %d) * %d)::bigint", intervalSec, intervalSec)
		}
	default: // SQLite and others
		bucketExpr = fmt.Sprintf("(cast(strftime('%%s', request_ts) as integer) / %d) * %d", intervalSec, intervalSec)
	}

	type overTimeSQLRow struct {
		BucketSec int64 `gorm:"column:bucket_sec"`
		Total     int   `gorm:"column:total"`
		Blocked   int   `gorm:"column:blocked"`
		Cached    int   `gorm:"column:cached"`
	}

	var rows []overTimeSQLRow
	selectClause := fmt.Sprintf("%s AS bucket_sec, COUNT(*) AS total, SUM(CASE WHEN response_type = 'BLOCKED' OR reason LIKE 'BLOCKED%%' THEN 1 ELSE 0 END) AS blocked, SUM(CASE WHEN response_type = 'CACHED' THEN 1 ELSE 0 END) AS cached", bucketExpr)
	err := r.applyFilter(r.db.Model(&logEntry{}), f).
		Select(selectClause).
		Group("bucket_sec").
		Order("bucket_sec ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	rowMap := make(map[int64]overTimeSQLRow, len(rows))
	for _, row := range rows {
		rowMap[row.BucketSec] = row
	}

	if f != nil && !f.From.IsZero() && !f.To.IsZero() {
		startSec := (f.From.Unix() / intervalSec) * intervalSec
		endSec := (f.To.Unix() / intervalSec) * intervalSec
		if endSec < startSec {
			endSec = startSec
		}
		capacity := int((endSec-startSec)/intervalSec) + 1
		if capacity > 1000 {
			capacity = 1000
		}
		result := make([]OverTimeBucket, 0, capacity)
		for sec := startSec; sec <= endSec; sec += intervalSec {
			t := time.Unix(sec, 0).UTC()
			if row, ok := rowMap[sec]; ok {
				result = append(result, OverTimeBucket{
					Time:    t,
					Total:   row.Total,
					Blocked: row.Blocked,
					Cached:  row.Cached,
				})
			} else {
				result = append(result, OverTimeBucket{
					Time:    t,
					Total:   0,
					Blocked: 0,
					Cached:  0,
				})
			}
		}

		return result, nil
	}

	if len(rows) == 0 {
		return []OverTimeBucket{}, nil
	}

	if len(rows) == 1 {
		startSec := rows[0].BucketSec - intervalSec
		endSec := rows[0].BucketSec + intervalSec
		result := make([]OverTimeBucket, 0, 3)
		for sec := startSec; sec <= endSec; sec += intervalSec {
			t := time.Unix(sec, 0).UTC()
			if row, ok := rowMap[sec]; ok {
				result = append(result, OverTimeBucket{
					Time:    t,
					Total:   row.Total,
					Blocked: row.Blocked,
					Cached:  row.Cached,
				})
			} else {
				result = append(result, OverTimeBucket{
					Time:    t,
					Total:   0,
					Blocked: 0,
					Cached:  0,
				})
			}
		}

		return result, nil
	}

	startSec := rows[0].BucketSec
	endSec := rows[len(rows)-1].BucketSec
	result := make([]OverTimeBucket, 0, int((endSec-startSec)/intervalSec)+1)
	for sec := startSec; sec <= endSec; sec += intervalSec {
		t := time.Unix(sec, 0).UTC()
		if row, ok := rowMap[sec]; ok {
			result = append(result, OverTimeBucket{
				Time:    t,
				Total:   row.Total,
				Blocked: row.Blocked,
				Cached:  row.Cached,
			})
		} else {
			result = append(result, OverTimeBucket{
				Time:    t,
				Total:   0,
				Blocked: 0,
				Cached:  0,
			})
		}
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
	type topRow struct {
		Name    string `gorm:"column:name"`
		Total   int    `gorm:"column:total"`
		Blocked int    `gorm:"column:blocked"`
	}

	nameExpr := "question_name"
	filterCond := "question_name != '' AND question_name IS NOT NULL"
	if byClient {
		nameExpr = "COALESCE(NULLIF(client_name, ''), client_ip)"
		filterCond = "COALESCE(NULLIF(client_name, ''), client_ip) IS NOT NULL AND COALESCE(NULLIF(client_name, ''), client_ip) != ''"
	}

	q := r.applyFilter(r.db.Model(&logEntry{}), f).Where(filterCond)
	if blockedOnly {
		q = q.Where("response_type = 'BLOCKED' OR reason LIKE 'BLOCKED%'")
	}

	var rows []topRow
	selectClause := fmt.Sprintf("%s AS name, COUNT(*) AS total, SUM(CASE WHEN response_type = 'BLOCKED' OR reason LIKE 'BLOCKED%%' THEN 1 ELSE 0 END) AS blocked", nameExpr)
	err := q.Select(selectClause).
		Group(nameExpr).
		Order("total DESC, name ASC").
		Limit(limit + 1).
		Offset(offset).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}

	var totalDistinct int64
	if len(rows) <= limit {
		totalDistinct = int64(offset + len(rows))
	} else {
		rows = rows[:limit]

		maxScan := offset + 101
		cq := r.applyFilter(r.db.Model(&logEntry{}), f).Where(filterCond)
		if blockedOnly {
			cq = cq.Where("response_type = 'BLOCKED' OR reason LIKE 'BLOCKED%'")
		}
		subQuery := cq.Select("1").Group(nameExpr).Limit(maxScan)
		var boundedCount int64
		if err := r.db.Table("(?) as sub", subQuery).Count(&boundedCount).Error; err == nil && boundedCount > 0 {
			totalDistinct = boundedCount
		} else {
			totalDistinct = int64(offset + limit + 1)
		}
	}

	items := make([]TopEntry, 0, len(rows))
	for _, row := range rows {
		items = append(items, TopEntry{
			Name:    row.Name,
			Total:   row.Total,
			Blocked: row.Blocked,
		})
	}

	return items, int(totalDistinct), nil
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

	nameExpr := "question_name"
	filterCond := "question_name != '' AND question_name IS NOT NULL"
	if byClient {
		nameExpr = "COALESCE(NULLIF(client_name, ''), client_ip)"
		filterCond = "COALESCE(NULLIF(client_name, ''), client_ip) IS NOT NULL AND COALESCE(NULLIF(client_name, ''), client_ip) != ''"
	}

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
		items = append(items, Suggestion{
			Name:  row.Name,
			Count: row.Count,
		})
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
		items = append(items, QueryTypeCount{
			Type:  row.Type,
			Count: row.Count,
		})
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
