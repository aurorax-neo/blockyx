package querylog

import (
	"strconv"
	"strings"
	"time"

	"github.com/0xERR0R/blocky/config"
	"github.com/0xERR0R/blocky/log"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func rollupLog() *logrus.Entry {
	return log.PrefixedLog("querylog_rollup")
}

func createRollupTables(db *gorm.DB, dbType config.QueryLogType) error {
	if err := db.Exec(counterCreateSQL(dbType)).Error; err != nil {
		return err
	}
	if err := db.Exec(topCreateSQL(dbType, hourTableName, hourTableComment)).Error; err != nil {
		return err
	}
	if err := db.Exec(topCreateSQL(dbType, recentTableName, recentTableComment)).Error; err != nil {
		return err
	}

	applyRollupComments(db, dbType)

	return nil
}

func counterCreateSQL(dbType config.QueryLogType) string {
	stmt := "CREATE TABLE IF NOT EXISTS " + counterTableName +
		" (bucket_sec BIGINT NOT NULL, total BIGINT NOT NULL, blocked BIGINT NOT NULL, " +
		"cached BIGINT NOT NULL, PRIMARY KEY (bucket_sec))"
	if dbType == config.QueryLogTypeMysql {
		return stmt + " COMMENT='" + counterTableComment + "'"
	}

	return stmt
}

func topCreateSQL(dbType config.QueryLogType, table, comment string) string {
	stmt := "CREATE TABLE IF NOT EXISTS " + table + " (kind INTEGER NOT NULL, bucket_sec BIGINT NOT NULL, " +
		"name VARCHAR(" + strconv.Itoa(rollupNameLen) + ") NOT NULL, total BIGINT NOT NULL, " +
		"blocked BIGINT NOT NULL, PRIMARY KEY (kind, bucket_sec, name))"
	if dbType == config.QueryLogTypeMysql {
		return stmt + " COMMENT='" + comment + "'"
	}

	return stmt
}

func applyRollupComments(db *gorm.DB, dbType config.QueryLogType) {
	if dbType != config.QueryLogTypePostgresql && dbType != config.QueryLogTypeTimescale {
		return
	}

	_ = db.Exec(commentSQL(counterTableName, counterTableComment)).Error
	_ = db.Exec(commentSQL(hourTableName, hourTableComment)).Error
	_ = db.Exec(commentSQL(recentTableName, recentTableComment)).Error
}

func commentSQL(table, comment string) string {
	return "COMMENT ON TABLE " + table + " IS '" + comment + "'"
}

func ensureRollupState(db *gorm.DB, dbType config.QueryLogType) error {
	if err := db.Exec(stateCreateSQL()).Error; err != nil {
		return err
	}

	now := time.Now().UTC().Unix()

	return db.Exec(
		stateSeedSQL(dbType),
		rollupStateID,
		alignDown(now, rollupBucketSec),
		alignDown(now, topHourSec),
	).Error
}

func stateCreateSQL() string {
	return "CREATE TABLE IF NOT EXISTS " + stateTableName +
		" (id INTEGER PRIMARY KEY, covered_counter BIGINT NOT NULL, covered_hour BIGINT NOT NULL)"
}

func stateSeedSQL(dbType config.QueryLogType) string {
	if dbType == config.QueryLogTypeMysql {
		return "INSERT IGNORE INTO " + stateTableName +
			" (id, covered_counter, covered_hour) VALUES (?, ?, ?)"
	}

	return "INSERT INTO " + stateTableName +
		" (id, covered_counter, covered_hour) VALUES (?, ?, ?) ON CONFLICT (id) DO NOTHING"
}

func stateLoadSQL() string {
	return "SELECT covered_counter, covered_hour FROM " + stateTableName + " WHERE id = ?"
}

func counterUpsertSQL(dbType config.QueryLogType, add bool) string {
	insert := "INSERT INTO " + counterTableName + " (bucket_sec, total, blocked, cached) VALUES (?, ?, ?, ?)"
	if dbType == config.QueryLogTypeMysql {
		return insert + mysqlCounterUpdate(add)
	}

	return insert + conflictCounterUpdate(add)
}

func mysqlCounterUpdate(add bool) string {
	if add {
		return " ON DUPLICATE KEY UPDATE total=total+VALUES(total), " +
			"blocked=blocked+VALUES(blocked), cached=cached+VALUES(cached)"
	}

	return " ON DUPLICATE KEY UPDATE total=VALUES(total), blocked=VALUES(blocked), cached=VALUES(cached)"
}

func conflictCounterUpdate(add bool) string {
	if add {
		return " ON CONFLICT (bucket_sec) DO UPDATE SET total=" + counterTableName + ".total+excluded.total, " +
			"blocked=" + counterTableName + ".blocked+excluded.blocked, " +
			"cached=" + counterTableName + ".cached+excluded.cached"
	}

	return " ON CONFLICT (bucket_sec) DO UPDATE SET total=excluded.total, " +
		"blocked=excluded.blocked, cached=excluded.cached"
}

func topUpsertSQL(dbType config.QueryLogType, table string, add bool) string {
	insert := "INSERT INTO " + table + " (kind, bucket_sec, name, total, blocked) VALUES (?, ?, ?, ?, ?)"
	if dbType == config.QueryLogTypeMysql {
		return insert + mysqlTopUpdate(add)
	}

	return insert + conflictTopUpdate(table, add)
}

func mysqlTopUpdate(add bool) string {
	if add {
		return " ON DUPLICATE KEY UPDATE total=total+VALUES(total), blocked=blocked+VALUES(blocked)"
	}

	return " ON DUPLICATE KEY UPDATE total=VALUES(total), blocked=VALUES(blocked)"
}

func conflictTopUpdate(table string, add bool) string {
	if add {
		return " ON CONFLICT (kind, bucket_sec, name) DO UPDATE SET total=" + table + ".total+excluded.total, " +
			"blocked=" + table + ".blocked+excluded.blocked"
	}

	return " ON CONFLICT (kind, bucket_sec, name) DO UPDATE SET total=excluded.total, blocked=excluded.blocked"
}

func blockedCaseSQL() string {
	return "SUM(CASE WHEN response_type IN ('" + responseBlocked + "', '" + responseRebind +
		"') THEN 1 ELSE 0 END)"
}

func cachedCaseSQL() string {
	return "SUM(CASE WHEN response_type = '" + responseCached + "' THEN 1 ELSE 0 END)"
}

func entryBucketSQL(dbType config.QueryLogType, step int) string {
	text := strconv.Itoa(step)
	switch dbType {
	case config.QueryLogTypeMysql:
		return "FLOOR(UNIX_TIMESTAMP(request_ts)/" + text + ")*" + text
	case config.QueryLogTypePostgresql, config.QueryLogTypeTimescale:
		return "floor(extract(epoch from request_ts AT TIME ZONE 'UTC')/" + text + ")*" + text
	case config.QueryLogTypeSqlite:
		fallthrough
	default:
		return "cast(strftime('%s', request_ts) as integer)/" + text + "*" + text
	}
}

func isMissingRelation(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()

	return strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "does not exist")
}
