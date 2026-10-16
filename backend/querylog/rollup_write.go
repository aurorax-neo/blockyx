package querylog

import (
	"fmt"
	"time"

	"github.com/0xERR0R/blocky/config"
	"github.com/0xERR0R/blocky/log"
)

func (d *DatabaseWriter) prepareRollup() error {
	if err := createRollupTables(d.db, d.dbType); err != nil {
		return err
	}

	d.rollupWarned = false

	return nil
}

func (d *DatabaseWriter) warnRollupSchema(err error) {
	if d.rollupWarned {
		return
	}

	d.rollupWarned = true
	log.PrefixedLog("database_writer").Warnf("query log rollup schema not ready, will retry: %v", err)
}

func (d *DatabaseWriter) flushPending() error {
	for offset := 0; offset < len(d.pendingEntries); {
		end := min(offset+entryBulkSize, len(d.pendingEntries))
		if err := d.writeBatch(d.pendingEntries[offset:end]); err != nil {
			if offset > 0 {
				d.keepPending(offset)
			}

			return fmt.Errorf("failed to write querylog entries to database: %w", err)
		}

		offset = end
	}

	d.pendingEntries = nil

	return nil
}

func (d *DatabaseWriter) writeBatch(batch []*logEntry) error {
	tx := d.db.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	if err := tx.Create(batch).Error; err != nil {
		_ = tx.Rollback()

		return err
	}
	if err := upsertLiveRollups(tx, d.dbType, batch); err != nil {
		_ = tx.Rollback()

		return err
	}
	if err := tx.Commit().Error; err != nil {
		_ = tx.Rollback()

		return err
	}

	return nil
}

func (d *DatabaseWriter) keepPending(offset int) {
	if offset <= 0 || offset >= len(d.pendingEntries) {
		return
	}

	tail := make([]*logEntry, len(d.pendingEntries)-offset)
	copy(tail, d.pendingEntries[offset:])
	d.pendingEntries = tail
}

func (d *DatabaseWriter) deleteExpiredEntries(deletionDate time.Time) {
	// Timescale drops log_entries itself. Rollup tables are plain tables.
	if d.dbType == config.QueryLogTypeTimescale {
		return
	}

	log.PrefixedLog("database_writer").Debugf("deleting log entries with request_ts < %s", deletionDate)
	d.db.Where("request_ts < ?", deletionDate).Delete(&logEntry{})
}

func (d *DatabaseWriter) deleteRollupBuckets(table string, cutoff int64) {
	err := d.db.Exec("DELETE FROM "+table+" WHERE bucket_sec < ?", cutoff).Error
	if err == nil {
		return
	}
	if isMissingRelation(err) {
		log.PrefixedLog("database_writer").Debugf("rollup cleanup skipped: %v", err)

		return
	}

	log.PrefixedLog("database_writer").Warnf("rollup cleanup failed: %v", err)
}

func (d *DatabaseWriter) pruneRecent() {
	cutoff := alignDown(time.Now().UTC().Unix(), topRecentSec) - int64(topRecentKeep/time.Second)
	if cutoff <= 0 {
		return
	}

	d.deleteRollupBuckets(recentTableName, cutoff)
}
