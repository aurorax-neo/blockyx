package querylog

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Count returns the total number of entries matching the filter.
func (r *DBReader) Count(f *Filter) (int64, error) {
	if !canUseRollup(f) {
		return r.rawCount(f)
	}

	err := r.waitRollup(func() bool {
		return r.coveredCounter <= coveredNeed(f)
	})
	if err != nil {
		if !shouldFallbackRollup(err) {
			return 0, err
		}

		r.noteRollupErr(err)

		return r.rawCount(f)
	}

	count, err := r.rollupCount(f)
	if err == nil || !shouldFallbackRollup(err) {
		return count, err
	}

	r.noteRollupErr(err)

	return r.rawCount(f)
}

func (r *DBReader) rawCount(f *Filter) (int64, error) {
	var count int64
	db := r.applyFilter(r.db.Model(&logEntry{}), f).Count(&count)

	return count, db.Error
}

func (r *DBReader) rollupCount(f *Filter) (int64, error) {
	stmt := "SELECT COALESCE(SUM(total), 0) FROM " + counterTableName + " WHERE 1 = 1"
	args := make([]any, 0, 2)
	stmt, args = appendCounterWindow(stmt, args, f)
	var n rollupInt
	err := r.db.WithContext(context.Background()).Raw(stmt, args...).Row().Scan(&n)
	if err != nil {
		return 0, err
	}

	return int64(n), nil
}

// OverTime returns per-interval aggregates.
func (r *DBReader) OverTime(f *Filter, interval time.Duration) ([]OverTimeBucket, error) {
	intervalSec := int64(interval.Seconds())
	if intervalSec <= 0 {
		intervalSec = defaultIntervalSec
	}
	if !canUseRollup(f) || !intervalAligned(intervalSec) {
		return r.rawOverTime(f, intervalSec)
	}

	err := r.waitRollup(func() bool {
		return r.coveredCounter <= coveredNeed(f)
	})
	if err != nil {
		if !shouldFallbackRollup(err) {
			return nil, err
		}

		r.noteRollupErr(err)

		return r.rawOverTime(f, intervalSec)
	}

	points, err := r.rollupOverTime(f, intervalSec)
	if err != nil {
		if !shouldFallbackRollup(err) {
			return nil, err
		}

		r.noteRollupErr(err)

		return r.rawOverTime(f, intervalSec)
	}

	return assembleOverTime(points, f, intervalSec), nil
}

func intervalAligned(intervalSec int64) bool {
	return intervalSec > 0 && intervalSec%rollupBucketSec == 0
}

func (r *DBReader) rollupOverTime(f *Filter, intervalSec int64) ([]overTimePoint, error) {
	expr := scaledBucketExpr(intervalSec)
	stmt := "SELECT " + expr + " AS bucket_sec, SUM(total) AS total, SUM(blocked) AS blocked, " +
		"SUM(cached) AS cached FROM " + counterTableName + " WHERE 1 = 1"
	args := make([]any, 0, 2)
	stmt, args = appendCounterWindow(stmt, args, f)
	stmt += " GROUP BY " + expr + " ORDER BY bucket_sec"
	var rows []rollupOverTimeRow
	err := r.db.WithContext(context.Background()).Raw(stmt, args...).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	points := make([]overTimePoint, 0, len(rows))
	for _, row := range rows {
		points = append(points, overTimePoint{
			BucketSec: int64(row.BucketSec),
			Total:     int(row.Total),
			Blocked:   int(row.Blocked),
			Cached:    int(row.Cached),
		})
	}

	return points, nil
}

func scaledBucketExpr(intervalSec int64) string {
	step := strconv.FormatInt(intervalSec, decimalBase)

	return "(bucket_sec/" + step + ")*" + step
}

func appendCounterWindow(stmt string, args []any, f *Filter) (string, []any) {
	if f == nil {
		return stmt, args
	}
	if !f.From.IsZero() {
		stmt += " AND bucket_sec >= ?"
		args = append(args, alignDown(f.From.Unix(), rollupBucketSec))
	}
	if !f.To.IsZero() {
		stmt += " AND bucket_sec < ?"
		args = append(args, alignDown(f.To.Unix(), rollupBucketSec))
	}

	return stmt, args
}

// Top returns the most frequent domains or clients.
func (r *DBReader) Top(f *Filter, byClient bool, blockedOnly bool, limit, offset int) ([]TopEntry, int, error) {
	if !canUseRollup(f) {
		return r.rawTop(f, byClient, blockedOnly, limit, offset)
	}

	err := r.waitTop(f)
	if err != nil {
		if !shouldFallbackRollup(err) {
			return nil, 0, err
		}

		r.noteRollupErr(err)

		return r.rawTop(f, byClient, blockedOnly, limit, offset)
	}

	items, total, err := r.rollupTop(f, byClient, blockedOnly, limit, offset)
	if err == nil || !shouldFallbackRollup(err) {
		return items, total, err
	}

	r.noteRollupErr(err)

	return r.rawTop(f, byClient, blockedOnly, limit, offset)
}

func (r *DBReader) waitTop(f *Filter) error {
	switch topModeOf(f) {
	case topRecent:
		return r.waitRollup(func() bool { return r.recentReady })
	case topHours:
		return r.waitRollup(func() bool { return r.coveredHour <= 0 })
	default:
		need := int64(0)
		if f != nil && !f.From.IsZero() {
			need = alignDown(f.From.Unix(), topHourSec)
		}

		return r.waitRollup(func() bool {
			return r.recentReady && r.coveredHour <= need
		})
	}
}

func topModeOf(f *Filter) int {
	if f == nil || f.From.IsZero() || f.To.IsZero() {
		return topHours
	}

	span := f.To.Unix() - f.From.Unix()
	recentFrom := time.Now().UTC().Add(-topRecentKeep).Unix()
	if span > 0 && span <= topHybridMaxSec && f.From.Unix() >= recentFrom &&
		alignedTime(f.From, topRecentSec) && alignedTime(f.To, topRecentSec) {
		return topRecent
	}

	return topHybrid
}

func (r *DBReader) rollupTop(
	f *Filter, byClient bool, blockedOnly bool, limit, offset int,
) ([]TopEntry, int, error) {
	rows, err := r.queryRollupTop(f, byClient, blockedOnly, limit+1, offset)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) <= limit {
		return rollupTopItems(rows), offset + len(rows), nil
	}

	rows = rows[:limit]
	total, countErr := r.rollupTopCount(f, byClient, blockedOnly, offset+topCountSlack)
	if countErr != nil || total <= 0 {
		return rollupTopItems(rows), offset + limit + 1, nil
	}

	return rollupTopItems(rows), total, nil
}

func (r *DBReader) queryRollupTop(
	f *Filter, byClient bool, blockedOnly bool, limit, offset int,
) ([]topRollupRow, error) {
	stmt, args := r.topSelectSQL(f, byClient, blockedOnly)
	stmt += " ORDER BY total DESC, name ASC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	var rows []topRollupRow
	err := r.db.WithContext(context.Background()).Raw(stmt, args...).Scan(&rows).Error

	return rows, err
}

func (r *DBReader) rollupTopCount(f *Filter, byClient bool, blockedOnly bool, maxScan int) (int, error) {
	body, args := r.topBodySQL(f, byClient, blockedOnly)
	stmt := "SELECT COUNT(*) FROM (SELECT 1 " + body + " LIMIT ?) AS sub"
	args = append(args, maxScan)
	var n rollupInt
	err := r.db.WithContext(context.Background()).Raw(stmt, args...).Row().Scan(&n)

	return int(n), err
}

func (r *DBReader) topSelectSQL(f *Filter, byClient, blockedOnly bool) (string, []any) {
	body, args := r.topBodySQL(f, byClient, blockedOnly)
	totalExpr := "SUM(total)"
	if blockedOnly {
		totalExpr = "SUM(blocked)"
	}

	return "SELECT name, " + totalExpr + " AS total, SUM(blocked) AS blocked " + body, args
}

func (r *DBReader) topBodySQL(f *Filter, byClient, blockedOnly bool) (string, []any) {
	kind := rollupKindDomain
	if byClient {
		kind = rollupKindClient
	}

	having := ""
	if blockedOnly {
		having = " HAVING SUM(blocked) > 0"
	}

	switch topModeOf(f) {
	case topRecent:
		from, to := recentWindow(f)
		stmt := "FROM " + recentTableName + " WHERE kind = ? AND name <> '' AND bucket_sec >= ? AND bucket_sec < ?"
		stmt += " GROUP BY name" + having

		return stmt, []any{kind, from, to}
	case topHours:
		stmt := "FROM " + hourTableName + " WHERE kind = ? AND name <> ''"
		args := []any{kind}
		stmt, args = appendHourWindow(stmt, args, f)
		stmt += " GROUP BY name" + having

		return stmt, args
	default:
		return hybridBody(kind, having, f)
	}
}

func recentWindow(f *Filter) (int64, int64) {
	return alignDown(f.From.Unix(), topRecentSec), alignDown(f.To.Unix(), topRecentSec)
}

func hybridBody(kind int, having string, f *Filter) (string, []any) {
	hourFrom := alignDown(f.From.Unix(), topHourSec)
	hourTo := alignDown(f.To.Unix(), topHourSec)
	if f.To.Unix() <= hourTo {
		stmt := "FROM " + hourTableName + " WHERE kind = ? AND name <> '' AND bucket_sec >= ? AND bucket_sec < ?"
		stmt += " GROUP BY name" + having

		return stmt, []any{kind, hourFrom, hourTo}
	}

	stmt := "FROM (SELECT name, total, blocked FROM " + hourTableName +
		" WHERE kind = ? AND name <> '' AND bucket_sec >= ? AND bucket_sec < ? UNION ALL " +
		"SELECT name, total, blocked FROM " + recentTableName +
		" WHERE kind = ? AND name <> '' AND bucket_sec >= ? AND bucket_sec < ?) AS rollup_top"
	stmt += " GROUP BY name" + having

	return stmt, []any{kind, hourFrom, hourTo, kind, hourTo, f.To.Unix()}
}

func appendHourWindow(stmt string, args []any, f *Filter) (string, []any) {
	if f == nil {
		return stmt, args
	}
	if !f.From.IsZero() {
		stmt += " AND bucket_sec >= ?"
		args = append(args, alignDown(f.From.Unix(), topHourSec))
	}
	if !f.To.IsZero() {
		stmt += " AND bucket_sec < ?"
		args = append(args, alignDown(f.To.Unix(), topHourSec))
	}

	return stmt, args
}

func rollupTopItems(rows []topRollupRow) []TopEntry {
	items := make([]TopEntry, 0, len(rows))
	for _, row := range rows {
		items = append(items, TopEntry{
			Name:    row.Name,
			Total:   int(row.Total),
			Blocked: int(row.Blocked),
		})
	}

	return items
}

func canUseRollup(f *Filter) bool {
	if f == nil {
		return true
	}
	if f.Domain != "" || f.Client != "" || f.Search != "" || f.QuestionType != "" || f.ResponseType != "" {
		return false
	}
	if !alignedTime(f.From, rollupBucketSec) || !alignedTime(f.To, rollupBucketSec) {
		return false
	}

	return true
}

func coveredNeed(f *Filter) int64 {
	if f == nil || f.From.IsZero() {
		return 0
	}

	return alignDown(f.From.Unix(), rollupBucketSec)
}

func (r *DBReader) waitRollup(ready func() bool) error {
	r.rollupMu.Lock()
	defer r.rollupMu.Unlock()

	for {
		if r.backfillStopped {
			return errRollupWindow{}
		}
		if r.rollupErr != nil {
			return r.rollupErr
		}
		if r.rollupReady && ready() {
			return nil
		}

		r.rollupCond.Wait()
	}
}

func shouldFallbackRollup(err error) bool {
	if err == nil {
		return false
	}

	var stop errRollupStop
	if errors.As(err, &stop) {
		return false
	}

	var window errRollupWindow
	if errors.As(err, &window) || errors.Is(err, context.Canceled) {
		return false
	}

	return true
}

func (r *DBReader) noteRollupErr(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		rollupLog().Warnf("query log rollup fallback: %v", err)

		return
	}

	rollupLog().Debugf("query log rollup fallback: %v", err)
}
