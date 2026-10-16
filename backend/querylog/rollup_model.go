package querylog

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"time"
)

// errRollupStop aborts backfill so a refresh does not fall back to a raw scan.
type errRollupStop struct {
	cause error
}

func (e errRollupStop) Error() string {
	if e.cause == nil {
		return "query log rollup backfill stopped"
	}

	return "query log rollup backfill stopped: " + e.cause.Error()
}

func (e errRollupStop) Unwrap() error {
	return e.cause
}

// errRollupWindow means the requested range is not covered and must not scan raw logs.
type errRollupWindow struct{}

func (errRollupWindow) Error() string {
	return "query log rollup window is not covered"
}

type backfillStepResult int

type rollupInt int64

type nullUnix struct {
	valid bool
	unix  int64
}

type rollupFlags struct {
	total   int64
	blocked int64
	cached  int64
}

type rollupKey struct {
	kind   int
	bucket int64
	name   string
}

type counterRow struct {
	bucket  int64
	total   int64
	blocked int64
	cached  int64
}

type topRow struct {
	kind    int
	bucket  int64
	name    string
	total   int64
	blocked int64
}

type counterScanRow struct {
	BucketSec rollupInt `gorm:"column:bucket_sec"`
	Total     rollupInt `gorm:"column:total"`
	Blocked   rollupInt `gorm:"column:blocked"`
	Cached    rollupInt `gorm:"column:cached"`
}

type topScanRow struct {
	Kind      rollupInt      `gorm:"column:kind"`
	BucketSec rollupInt      `gorm:"column:bucket_sec"`
	Name      sql.NullString `gorm:"column:name"`
	Total     rollupInt      `gorm:"column:total"`
	Blocked   rollupInt      `gorm:"column:blocked"`
}

type rollupOverTimeRow struct {
	BucketSec rollupInt `gorm:"column:bucket_sec"`
	Total     rollupInt `gorm:"column:total"`
	Blocked   rollupInt `gorm:"column:blocked"`
	Cached    rollupInt `gorm:"column:cached"`
}

type topRollupRow struct {
	Name       string    `gorm:"column:name"`
	Total      rollupInt `gorm:"column:total"`
	Blocked    rollupInt `gorm:"column:blocked"`
	TotalCount rollupInt `gorm:"column:total_count"`
}

type overTimePoint struct {
	BucketSec int64
	Total     int
	Blocked   int
	Cached    int
}

const (
	backfillContinue backfillStepResult = iota
	backfillDone
	backfillCancel

	backfillFailed backfillStepResult = -1

	rollupBucketSec    = 30
	topHourSec         = 3600
	topRecentSec       = 300
	topRecentKeep      = 3 * time.Hour
	topHybridMaxSec    = 2 * topHourSec
	rollupNameLen      = 255
	rollupBulkSize     = 100
	entryBulkSize      = 100
	rollupChunkSec     = 21600
	defaultIntervalSec = 3600
	hourIntervalSec    = defaultIntervalSec
	minuteIntervalSec  = 60
	dayIntervalSec     = 86400
	overTimeFillCap    = 1000
	topCountSlack      = 101
	rollupStateID      = 1
	decimalBase        = 10
	int64BitSize       = 64

	rollupKindDomain = 1
	rollupKindClient = 2
	topRecent        = 1
	topHybrid        = 2
	topHours         = 3

	responseBlocked = "BLOCKED"
	responseRebind  = "REBIND"
	responseCached  = "CACHED"

	clientNameExpr  = "COALESCE(NULLIF(client_name, ''), client_ip)"
	questionNameCol = "question_name"

	counterTableName    = "log_counters"
	hourTableName       = "log_top_hours"
	recentTableName     = "log_top_recent"
	stateTableName      = "log_rollup_state"
	counterTableComment = "30s counters so dashboard refresh does not scan raw logs"
	hourTableComment    = "hourly name totals for top lists"
	recentTableComment  = "5m name totals kept only for the latest hours"
)

func alignDown(unix, step int64) int64 {
	if unix <= 0 || step <= 0 {
		return 0
	}

	return unix / step * step
}

func alignedUnix(unix, step int64) bool {
	return step > 0 && unix%step == 0
}

func alignedTime(t time.Time, step int64) bool {
	return t.IsZero() || alignedUnix(t.Unix(), step)
}

func (n *rollupInt) Scan(src any) error {
	parsed, err := scanRollupInt(src)
	if err != nil {
		return err
	}

	*n = rollupInt(parsed)

	return nil
}

func scanRollupInt(src any) (int64, error) {
	switch value := src.(type) {
	case nil:
		return 0, nil
	case int64:
		return value, nil
	case int32:
		return int64(value), nil
	case int:
		return int64(value), nil
	case uint64:
		return scanRollupUint(value)
	case float64:
		return int64(value), nil
	case float32:
		return int64(value), nil
	case string:
		return parseRollupText(value)
	case []byte:
		return parseRollupText(string(value))
	default:
		return 0, fmt.Errorf("unsupported rollup number %T", src)
	}
}

func scanRollupUint(value uint64) (int64, error) {
	if value > uint64(math.MaxInt64) {
		return 0, fmt.Errorf("rollup counter overflows int64")
	}

	return int64(value), nil
}

func parseRollupText(text string) (int64, error) {
	parsed, err := strconv.ParseInt(text, decimalBase, int64BitSize)
	if err == nil {
		return parsed, nil
	}

	asFloat, floatErr := strconv.ParseFloat(text, int64BitSize)
	if floatErr != nil {
		return 0, err
	}

	return int64(asFloat), nil
}

func (n *nullUnix) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		n.valid = false
		n.unix = 0

		return nil
	case time.Time:
		n.valid = true
		n.unix = value.UTC().Unix()

		return nil
	case string:
		return n.parse(value)
	case []byte:
		return n.parse(string(value))
	default:
		return fmt.Errorf("unsupported time value %T", src)
	}
}

func (n *nullUnix) parse(value string) error {
	parsed, err := parseDBTime(value)
	if err != nil {
		return err
	}

	n.valid = true
	n.unix = parsed.UTC().Unix()

	return nil
}

func parseDBTime(value string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02T15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("cannot parse database time %q", value)
}

func (row topScanRow) keyName() string {
	if !row.Name.Valid {
		return ""
	}

	return row.Name.String
}
