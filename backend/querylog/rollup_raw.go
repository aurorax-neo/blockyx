package querylog

import (
	"strconv"
	"time"

	"github.com/0xERR0R/blocky/config"
)

type overTimeSQLRow struct {
	BucketSec int64 `gorm:"column:bucket_sec"`
	Total     int   `gorm:"column:total"`
	Blocked   int   `gorm:"column:blocked"`
	Cached    int   `gorm:"column:cached"`
}

func (r *DBReader) rawOverTime(f *Filter, intervalSec int64) ([]OverTimeBucket, error) {
	bucketExpr := rawBucketExpr(r.dbType, intervalSec)
	selectClause := bucketExpr + " AS bucket_sec, COUNT(*) AS total, " +
		blockedCaseSQL() + " AS blocked, " + cachedCaseSQL() + " AS cached"
	var rows []overTimeSQLRow
	err := r.applyFilter(r.db.Model(&logEntry{}), f).
		Select(selectClause).
		Group(bucketExpr).
		Order("bucket_sec ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	return assembleOverTime(rawOverTimePoints(rows), f, intervalSec), nil
}

func rawOverTimePoints(rows []overTimeSQLRow) []overTimePoint {
	points := make([]overTimePoint, 0, len(rows))
	for _, row := range rows {
		points = append(points, overTimePoint{
			BucketSec: row.BucketSec,
			Total:     row.Total,
			Blocked:   row.Blocked,
			Cached:    row.Cached,
		})
	}

	return points
}

func rawBucketExpr(dbType config.QueryLogType, intervalSec int64) string {
	switch dbType {
	case config.QueryLogTypeMysql:
		return mysqlBucketExpr(intervalSec)
	case config.QueryLogTypePostgresql, config.QueryLogTypeTimescale:
		return postgresBucketExpr(intervalSec)
	case config.QueryLogTypeSqlite:
		fallthrough
	default:
		return sqliteBucketExpr(intervalSec)
	}
}

func mysqlBucketExpr(intervalSec int64) string {
	step := strconv.FormatInt(intervalSec, decimalBase)

	return "(FLOOR(UNIX_TIMESTAMP(request_ts) / " + step + ") * " + step + ")"
}

func sqliteBucketExpr(intervalSec int64) string {
	step := strconv.FormatInt(intervalSec, decimalBase)

	return "(cast(strftime('%s', request_ts) as integer) / " + step + ") * " + step
}

func postgresBucketExpr(intervalSec int64) string {
	switch intervalSec {
	case hourIntervalSec:
		return "extract(epoch from date_trunc('hour', request_ts AT TIME ZONE 'UTC'))::bigint"
	case dayIntervalSec:
		return "extract(epoch from date_trunc('day', request_ts AT TIME ZONE 'UTC'))::bigint"
	case minuteIntervalSec:
		return "extract(epoch from date_trunc('minute', request_ts AT TIME ZONE 'UTC'))::bigint"
	default:
		step := strconv.FormatInt(intervalSec, decimalBase)

		return "(floor(extract(epoch from (request_ts AT TIME ZONE 'UTC')) / " + step + ") * " + step + ")::bigint"
	}
}

func assembleOverTime(points []overTimePoint, f *Filter, intervalSec int64) []OverTimeBucket {
	if f != nil && !f.From.IsZero() && !f.To.IsZero() {
		return fillBounded(points, f.From.Unix(), f.To.Unix(), intervalSec)
	}

	return fillOpen(points, intervalSec)
}

func fillBounded(points []overTimePoint, fromUnix, toUnix, intervalSec int64) []OverTimeBucket {
	start := (fromUnix / intervalSec) * intervalSec
	end := (toUnix / intervalSec) * intervalSec
	if end < start {
		end = start
	}

	capacity := int((end-start)/intervalSec) + 1
	if capacity > overTimeFillCap {
		capacity = overTimeFillCap
	}

	return fillSpan(pointMap(points, intervalSec), start, end, intervalSec, capacity)
}

func fillOpen(points []overTimePoint, intervalSec int64) []OverTimeBucket {
	if len(points) == 0 {
		return []OverTimeBucket{}
	}

	mapped := pointMap(points, intervalSec)
	start := points[0].BucketSec
	end := points[len(points)-1].BucketSec
	if len(points) == 1 {
		start = points[0].BucketSec - intervalSec
		end = points[0].BucketSec + intervalSec
	}

	return fillSpan(mapped, start, end, intervalSec, 0)
}

func fillSpan(
	mapped map[int64]overTimePoint, start, end, intervalSec int64, capacity int,
) []OverTimeBucket {
	if capacity < 0 {
		capacity = 0
	}

	result := make([]OverTimeBucket, 0, capacity)
	for sec := start; sec <= end; sec += intervalSec {
		result = append(result, bucketAt(mapped, sec))
	}

	return result
}

func pointMap(points []overTimePoint, intervalSec int64) map[int64]overTimePoint {
	mapped := make(map[int64]overTimePoint, len(points))
	for _, point := range points {
		key := (point.BucketSec / intervalSec) * intervalSec
		mapped[key] = point
	}

	return mapped
}

func bucketAt(mapped map[int64]overTimePoint, sec int64) OverTimeBucket {
	stamp := time.Unix(sec, 0).UTC()
	row, ok := mapped[sec]
	if !ok {
		return OverTimeBucket{Time: stamp}
	}

	return OverTimeBucket{Time: stamp, Total: row.Total, Blocked: row.Blocked, Cached: row.Cached}
}

func (r *DBReader) rawTop(f *Filter, byClient bool, blockedOnly bool, limit, offset int) ([]TopEntry, int, error) {
	type topSQLRow struct {
		Name    string `gorm:"column:name"`
		Total   int    `gorm:"column:total"`
		Blocked int    `gorm:"column:blocked"`
	}

	nameExpr, filterCond := groupedName(byClient)
	query := r.applyFilter(r.db.Model(&logEntry{}), f).Where(filterCond)
	if blockedOnly {
		query = query.Where("response_type IN (?, ?)", responseBlocked, responseRebind)
	}

	var rows []topSQLRow
	err := query.Select(nameExpr + " AS name, COUNT(*) AS total, " + blockedCaseSQL() + " AS blocked").
		Group(nameExpr).
		Order("total DESC, name ASC").
		Limit(limit + 1).
		Offset(offset).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}

	totalDistinct := offset + len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
		totalDistinct = r.rawTopBound(f, nameExpr, filterCond, blockedOnly, limit, offset)
	}

	items := make([]TopEntry, 0, len(rows))
	for _, row := range rows {
		items = append(items, TopEntry{Name: row.Name, Total: row.Total, Blocked: row.Blocked})
	}

	return items, totalDistinct, nil
}

func (r *DBReader) rawTopBound(
	f *Filter, nameExpr, filterCond string, blockedOnly bool, limit, offset int,
) int {
	maxScan := offset + topCountSlack
	query := r.applyFilter(r.db.Model(&logEntry{}), f).Where(filterCond)
	if blockedOnly {
		query = query.Where("response_type IN (?, ?)", responseBlocked, responseRebind)
	}

	subQuery := query.Select("1").Group(nameExpr).Limit(maxScan)
	var boundedCount int64
	err := r.db.Table("(?) as sub", subQuery).Count(&boundedCount).Error
	if err == nil && boundedCount > 0 {
		return int(boundedCount)
	}

	return offset + limit + 1
}

func groupedName(byClient bool) (string, string) {
	if byClient {
		return clientNameExpr, clientNameExpr + " IS NOT NULL AND " + clientNameExpr + " != ''"
	}

	return questionNameCol, questionNameCol + " != '' AND " + questionNameCol + " IS NOT NULL"
}
