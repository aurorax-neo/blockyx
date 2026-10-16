package querylog

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

func (r *DBReader) startBackfill(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	r.rollupMu.Lock()
	r.cancelBackfill = cancel
	r.rollupMu.Unlock()
	go r.runBackfill(ctx)
}

func (r *DBReader) runBackfill(ctx context.Context) {
	defer close(r.backfillDone)

	if err := r.initRollup(ctx); err != nil {
		r.failInitUnlessCancel(ctx, err)

		return
	}
	if err := r.fillOpenEdge(ctx); err != nil {
		r.failInitUnlessCancel(ctx, err)

		return
	}

	r.markReady()
	r.backfillHistory(ctx)
}

func (r *DBReader) initRollup(ctx context.Context) error {
	db := r.db.WithContext(ctx)
	if err := createRollupTables(db, r.dbType); err != nil {
		return err
	}
	if err := ensureRollupState(db, r.dbType); err != nil {
		return err
	}

	return r.loadCovered(db)
}

func (r *DBReader) loadCovered(db *gorm.DB) error {
	var counter rollupInt
	var hour rollupInt
	err := db.Raw(stateLoadSQL(), rollupStateID).Row().Scan(&counter, &hour)
	if err != nil {
		return err
	}

	r.rollupMu.Lock()
	r.coveredCounter = int64(counter)
	r.coveredHour = int64(hour)
	r.rollupCond.Broadcast()
	r.rollupMu.Unlock()

	return nil
}

func (r *DBReader) fillOpenEdge(ctx context.Context) error {
	counterEnd := r.snapshotCounter()
	hourStart := alignDown(counterEnd, topHourSec)
	if counterEnd > hourStart {
		empty, err := r.rangeEmpty(ctx, counterTableName, hourStart, counterEnd)
		if err != nil {
			return err
		}
		if empty {
			if err = r.replaceCounters(ctx, hourStart, counterEnd); err != nil {
				return err
			}
		}
	}

	recentTo := alignDown(time.Now().UTC().Unix(), topRecentSec)
	recentFrom := recentTo - int64(topRecentKeep/time.Second)
	if recentTo > recentFrom {
		empty, err := r.rangeEmpty(ctx, recentTableName, recentFrom, recentTo)
		if err != nil {
			return err
		}
		if empty {
			if err = r.replaceRecent(ctx, recentFrom, recentTo); err != nil {
				return err
			}
		}
	}

	if counterEnd <= hourStart {
		return nil
	}

	empty, err := r.rangeEmpty(ctx, hourTableName, hourStart, hourStart+topHourSec)
	if err != nil || !empty {
		return err
	}

	return r.replaceHours(ctx, hourStart, counterEnd)
}

func (r *DBReader) rangeEmpty(ctx context.Context, table string, from, to int64) (bool, error) {
	var n rollupInt
	err := r.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM "+table+" WHERE bucket_sec >= ? AND bucket_sec < ?",
		from, to,
	).Row().Scan(&n)
	if err != nil {
		return false, err
	}

	return n == 0, nil
}

func (r *DBReader) backfillHistory(ctx context.Context) {
	didWork := false
	for {
		step, err := r.backfillStep(ctx, &didWork)
		switch step {
		case backfillContinue:
			continue
		case backfillDone:
			r.completeBackfill(didWork)

			return
		case backfillCancel:
			r.markBackfillStopped()

			return
		default:
			r.finishBackfillErr(ctx, err)

			return
		}
	}
}

func (r *DBReader) backfillStep(ctx context.Context, didWork *bool) (backfillStepResult, error) {
	if ctxCanceled(ctx, nil) {
		return backfillCancel, nil
	}
	if r.snapshotCounter() <= 0 && r.snapshotHour() <= 0 {
		return backfillDone, nil
	}

	advanced, err := r.stepCounters(ctx)
	if err != nil {
		return backfillFailed, err
	}
	if advanced {
		*didWork = true
	}

	advanced, err = r.stepHours(ctx)
	if err != nil {
		return backfillFailed, err
	}
	if advanced {
		*didWork = true
	}
	if r.snapshotCounter() <= 0 && r.snapshotHour() <= 0 {
		return backfillDone, nil
	}

	return backfillContinue, nil
}

func (r *DBReader) stepCounters(ctx context.Context) (bool, error) {
	covered := r.snapshotCounter()
	if covered <= 0 {
		return false, nil
	}

	return r.stepCoverage(ctx, covered, rollupBucketSec, r.persistCounter, r.replaceCounters)
}

func (r *DBReader) stepHours(ctx context.Context) (bool, error) {
	covered := r.snapshotHour()
	if covered <= 0 {
		return false, nil
	}

	return r.stepCoverage(ctx, covered, topHourSec, r.persistHour, r.replaceHours)
}

type coverageReplace func(context.Context, int64, int64) error

type coverageStore func(context.Context, int64) error

func (r *DBReader) stepCoverage(
	ctx context.Context, covered, step int64, store coverageStore, replace coverageReplace,
) (bool, error) {
	maxUnix, ok, err := r.maxBefore(ctx, covered)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, store(ctx, 0)
	}

	lower, upper, open := nextCoverageWindow(maxUnix, covered, step)
	if !open {
		return true, store(ctx, 0)
	}
	if err = replace(ctx, lower, upper); err != nil {
		return false, err
	}
	if err = store(ctx, lower); err != nil {
		return false, err
	}

	rollupLog().Debugf("rollup backfill chunk [%d, %d)", lower, upper)

	return true, nil
}

func nextCoverageWindow(maxUnix, covered, step int64) (int64, int64, bool) {
	upper := alignDown(maxUnix, step) + step
	if upper > covered {
		upper = covered
	}

	lower := upper - rollupChunkSec
	if lower < 0 {
		lower = 0
	}
	if upper <= lower {
		return 0, 0, false
	}

	return lower, upper, true
}

func (r *DBReader) maxBefore(ctx context.Context, covered int64) (int64, bool, error) {
	var maxTS nullUnix
	err := r.db.WithContext(ctx).Raw(
		"SELECT MAX(request_ts) FROM log_entries WHERE request_ts < ?",
		time.Unix(covered, 0).UTC(),
	).Row().Scan(&maxTS)
	if err != nil {
		return 0, false, wrapBackfillErr(ctx, err)
	}
	if !maxTS.valid {
		return 0, false, nil
	}

	return maxTS.unix, true, nil
}

func (r *DBReader) replaceCounters(ctx context.Context, lower, upper int64) error {
	rows, err := r.loadCounters(ctx, lower, upper)
	if err != nil {
		return err
	}

	return r.swapCounters(ctx, lower, upper, rows)
}

func (r *DBReader) replaceHours(ctx context.Context, lower, upper int64) error {
	rows, err := r.loadNames(ctx, topHourSec, lower, upper)
	if err != nil {
		return err
	}

	return r.swapTop(ctx, hourTableName, lower, upper, rows)
}

func (r *DBReader) replaceRecent(ctx context.Context, lower, upper int64) error {
	rows, err := r.loadNames(ctx, topRecentSec, lower, upper)
	if err != nil {
		return err
	}

	return r.swapTop(ctx, recentTableName, lower, upper, rows)
}

func (r *DBReader) swapCounters(ctx context.Context, lower, upper int64, rows []counterRow) error {
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return wrapBackfillErr(ctx, tx.Error)
	}

	err := tx.Exec(
		"DELETE FROM "+counterTableName+" WHERE bucket_sec >= ? AND bucket_sec < ?",
		lower, upper,
	).Error
	if err != nil {
		_ = tx.Rollback()

		return wrapBackfillErr(ctx, err)
	}
	if err = execCounterRows(tx, r.dbType, rows, false); err != nil {
		_ = tx.Rollback()

		return wrapBackfillErr(ctx, err)
	}
	if err = tx.Commit().Error; err != nil {
		_ = tx.Rollback()

		return wrapBackfillErr(ctx, err)
	}

	return nil
}

func (r *DBReader) swapTop(ctx context.Context, table string, lower, upper int64, rows []topRow) error {
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return wrapBackfillErr(ctx, tx.Error)
	}

	err := tx.Exec(
		"DELETE FROM "+table+" WHERE bucket_sec >= ? AND bucket_sec < ?",
		lower, upper,
	).Error
	if err != nil {
		_ = tx.Rollback()

		return wrapBackfillErr(ctx, err)
	}
	if err = execTopRows(tx, r.dbType, table, rows, false); err != nil {
		_ = tx.Rollback()

		return wrapBackfillErr(ctx, err)
	}
	if err = tx.Commit().Error; err != nil {
		_ = tx.Rollback()

		return wrapBackfillErr(ctx, err)
	}

	return nil
}

func (r *DBReader) loadCounters(ctx context.Context, lower, upper int64) ([]counterRow, error) {
	bucket := entryBucketSQL(r.dbType, rollupBucketSec)
	stmt := "SELECT " + bucket + " AS bucket_sec, COUNT(*) AS total, " + blockedCaseSQL() +
		" AS blocked, " + cachedCaseSQL() + " AS cached FROM log_entries " +
		"WHERE request_ts >= ? AND request_ts < ? GROUP BY " + bucket
	var scanned []counterScanRow
	err := r.db.WithContext(ctx).Raw(stmt, time.Unix(lower, 0).UTC(), time.Unix(upper, 0).UTC()).Scan(&scanned).Error
	if err != nil {
		return nil, wrapBackfillErr(ctx, err)
	}

	rows := make([]counterRow, 0, len(scanned))
	for _, row := range scanned {
		rows = append(rows, counterRow{
			bucket:  int64(row.BucketSec),
			total:   int64(row.Total),
			blocked: int64(row.Blocked),
			cached:  int64(row.Cached),
		})
	}

	return rows, nil
}

func (r *DBReader) loadNames(ctx context.Context, step int, lower, upper int64) ([]topRow, error) {
	domain, err := r.loadNameKind(ctx, step, rollupKindDomain, questionNameCol, lower, upper)
	if err != nil {
		return nil, err
	}

	clients, err := r.loadNameKind(ctx, step, rollupKindClient, clientNameExpr, lower, upper)
	if err != nil {
		return nil, err
	}

	return append(domain, clients...), nil
}

func (r *DBReader) loadNameKind(
	ctx context.Context, step, kind int, nameExpr string, lower, upper int64,
) ([]topRow, error) {
	stmt, err := nameKindSQL(r.dbType, step, kind, nameExpr)
	if err != nil {
		return nil, err
	}

	var scanned []topScanRow
	err = r.db.WithContext(ctx).Raw(
		stmt, time.Unix(lower, 0).UTC(), time.Unix(upper, 0).UTC(),
	).Scan(&scanned).Error
	if err != nil {
		return nil, wrapBackfillErr(ctx, err)
	}

	rows := make([]topRow, 0, len(scanned))
	for _, row := range scanned {
		rows = append(rows, topRow{
			kind:    int(row.Kind),
			bucket:  int64(row.BucketSec),
			name:    row.keyName(),
			total:   int64(row.Total),
			blocked: int64(row.Blocked),
		})
	}

	return rows, nil
}

func (r *DBReader) persistCounter(ctx context.Context, covered int64) error {
	return r.persistCoverage(ctx, "covered_counter", covered, func(value int64) {
		r.coveredCounter = value
	})
}

func (r *DBReader) persistHour(ctx context.Context, covered int64) error {
	return r.persistCoverage(ctx, "covered_hour", covered, func(value int64) {
		r.coveredHour = value
	})
}

func (r *DBReader) persistCoverage(
	ctx context.Context, column string, covered int64, assign func(int64),
) error {
	err := r.db.WithContext(ctx).Exec(
		"UPDATE "+stateTableName+" SET "+column+" = ? WHERE id = ?",
		covered, rollupStateID,
	).Error
	if err != nil {
		return err
	}

	r.rollupMu.Lock()
	assign(covered)
	r.rollupCond.Broadcast()
	r.rollupMu.Unlock()

	return nil
}

func (r *DBReader) snapshotCounter() int64 {
	r.rollupMu.Lock()
	defer r.rollupMu.Unlock()

	return r.coveredCounter
}

func (r *DBReader) snapshotHour() int64 {
	r.rollupMu.Lock()
	defer r.rollupMu.Unlock()

	return r.coveredHour
}

func (r *DBReader) markReady() {
	r.rollupMu.Lock()
	r.rollupReady = true
	r.recentReady = true
	r.rollupCond.Broadcast()
	r.rollupMu.Unlock()
}

func (r *DBReader) completeBackfill(didWork bool) {
	r.rollupMu.Lock()
	r.rollupCond.Broadcast()
	r.rollupMu.Unlock()
	if didWork {
		rollupLog().Info("query log rollup backfill reached 0")
	}
}

func (r *DBReader) failBackfill(err error) {
	r.rollupMu.Lock()
	r.rollupErr = err
	r.rollupCond.Broadcast()
	r.rollupMu.Unlock()
	rollupLog().Warnf("query log rollup backfill stopped: %v", err)
}

func (r *DBReader) markBackfillStopped() {
	r.rollupMu.Lock()
	r.backfillStopped = true
	r.rollupCond.Broadcast()
	r.rollupMu.Unlock()
}

func (r *DBReader) failInitUnlessCancel(ctx context.Context, err error) {
	if ctxCanceled(ctx, err) {
		r.rollupMu.Lock()
		r.rollupCond.Broadcast()
		r.rollupMu.Unlock()

		return
	}

	r.rollupMu.Lock()
	r.rollupErr = err
	r.rollupCond.Broadcast()
	r.rollupMu.Unlock()
	rollupLog().Warnf("rollup disabled (%s): %v", r.dbType, err)
}

func (r *DBReader) finishBackfillErr(ctx context.Context, err error) {
	wrapped := wrapBackfillErr(ctx, err)
	var stop errRollupStop
	if errors.As(wrapped, &stop) || errors.Is(wrapped, context.Canceled) {
		r.markBackfillStopped()

		return
	}

	r.failBackfill(wrapped)
}

func wrapBackfillErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}

	var stop errRollupStop
	if errors.As(err, &stop) {
		return err
	}
	if ctxCanceled(ctx, err) {
		return errRollupStop{cause: context.Canceled}
	}

	return err
}

func ctxCanceled(ctx context.Context, err error) bool {
	if err != nil && errors.Is(err, context.Canceled) {
		return true
	}

	return ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled)
}

func (r *DBReader) stopBackfill() {
	r.rollupMu.Lock()
	done := r.backfillDone
	if done == nil {
		r.rollupMu.Unlock()

		return
	}

	select {
	case <-done:
		r.rollupMu.Unlock()

		return
	default:
	}

	r.backfillStopped = true
	if r.rollupCond != nil {
		r.rollupCond.Broadcast()
	}

	cancel := r.cancelBackfill
	r.rollupMu.Unlock()
	if cancel != nil {
		cancel()
	}

	<-done
}
