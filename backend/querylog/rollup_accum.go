package querylog

import (
	"time"

	"github.com/0xERR0R/blocky/config"
	"gorm.io/gorm"
)

func upsertLiveRollups(tx *gorm.DB, dbType config.QueryLogType, batch []*logEntry) error {
	counters, hours, recent := accumulateRollups(batch, time.Now().UTC())
	if err := execCounterRows(tx, dbType, counters, true); err != nil {
		return err
	}
	if err := execTopRows(tx, dbType, hourTableName, hours, true); err != nil {
		return err
	}

	return execTopRows(tx, dbType, recentTableName, recent, true)
}

func execCounterRows(tx *gorm.DB, dbType config.QueryLogType, rows []counterRow, add bool) error {
	if len(rows) == 0 {
		return nil
	}

	stmt := counterUpsertSQL(dbType, add)
	for start := 0; start < len(rows); start += rollupBulkSize {
		end := min(start+rollupBulkSize, len(rows))
		if err := execCounterSlice(tx, stmt, rows[start:end]); err != nil {
			return err
		}
	}

	return nil
}

func execCounterSlice(tx *gorm.DB, stmt string, rows []counterRow) error {
	for _, row := range rows {
		err := tx.Exec(stmt, row.bucket, row.total, row.blocked, row.cached).Error
		if err != nil {
			return err
		}
	}

	return nil
}

func execTopRows(tx *gorm.DB, dbType config.QueryLogType, table string, rows []topRow, add bool) error {
	if len(rows) == 0 {
		return nil
	}

	stmt := topUpsertSQL(dbType, table, add)
	for start := 0; start < len(rows); start += rollupBulkSize {
		end := min(start+rollupBulkSize, len(rows))
		if err := execTopSlice(tx, stmt, rows[start:end]); err != nil {
			return err
		}
	}

	return nil
}

func execTopSlice(tx *gorm.DB, stmt string, rows []topRow) error {
	for _, row := range rows {
		err := tx.Exec(stmt, row.kind, row.bucket, row.name, row.total, row.blocked).Error
		if err != nil {
			return err
		}
	}

	return nil
}

func accumulateRollups(entries []*logEntry, now time.Time) ([]counterRow, []topRow, []topRow) {
	recentCut := alignDown(now.Unix(), topRecentSec) - int64(topRecentKeep/time.Second)
	counters := make(map[int64]*rollupFlags)
	hours := make(map[rollupKey]*rollupFlags)
	recent := make(map[rollupKey]*rollupFlags)
	for _, entry := range entries {
		unix := entry.RequestTS.UTC().Unix()
		flags := flagsOf(entry.ResponseType)
		addCounter(counters, alignDown(unix, rollupBucketSec), flags)
		addTop(hours, rollupKindDomain, alignDown(unix, topHourSec), entry.QuestionName, flags)
		addTop(hours, rollupKindClient, alignDown(unix, topHourSec), clientKey(entry), flags)
		bucket := alignDown(unix, topRecentSec)
		if bucket < recentCut {
			continue
		}

		addTop(recent, rollupKindDomain, bucket, entry.QuestionName, flags)
		addTop(recent, rollupKindClient, bucket, clientKey(entry), flags)
	}

	return counterRows(counters), topRows(hours), topRows(recent)
}

func clientKey(entry *logEntry) string {
	if entry.ClientName != "" {
		return entry.ClientName
	}

	return entry.ClientIP
}

func addCounter(acc map[int64]*rollupFlags, bucket int64, flags rollupFlags) {
	current := acc[bucket]
	if current == nil {
		current = &rollupFlags{}
		acc[bucket] = current
	}

	current.total += flags.total
	current.blocked += flags.blocked
	current.cached += flags.cached
}

func addTop(acc map[rollupKey]*rollupFlags, kind int, bucket int64, name string, flags rollupFlags) {
	if name == "" {
		return
	}

	key := rollupKey{kind: kind, bucket: bucket, name: name}
	current := acc[key]
	if current == nil {
		current = &rollupFlags{}
		acc[key] = current
	}

	current.total += flags.total
	current.blocked += flags.blocked
}

func counterRows(acc map[int64]*rollupFlags) []counterRow {
	rows := make([]counterRow, 0, len(acc))
	for bucket, flags := range acc {
		rows = append(rows, counterRow{
			bucket: bucket, total: flags.total, blocked: flags.blocked, cached: flags.cached,
		})
	}

	return rows
}

func topRows(acc map[rollupKey]*rollupFlags) []topRow {
	rows := make([]topRow, 0, len(acc))
	for key, flags := range acc {
		rows = append(rows, topRow{
			kind: key.kind, bucket: key.bucket, name: key.name, total: flags.total, blocked: flags.blocked,
		})
	}

	return rows
}

func flagsOf(responseType string) rollupFlags {
	flags := rollupFlags{total: 1}
	if responseType == responseBlocked || responseType == responseRebind {
		flags.blocked = 1
	}
	if responseType == responseCached {
		flags.cached = 1
	}

	return flags
}
