package querylog

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/0xERR0R/blocky/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("log rollup storage", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
	})

	It("keeps pending rows when the rollup schema cannot be created", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "closed.db")
		writer, writeErr := NewDatabaseWriter(ctx, config.QueryLogTypeSqlite, dbPath, time.Hour, time.Hour)
		Expect(writeErr).Should(Succeed())

		sqlDB, writeErr := writer.db.DB()
		Expect(writeErr).Should(Succeed())
		Expect(sqlDB.Close()).Should(Succeed())

		writer.Write(&LogEntry{Start: time.Now(), DurationMs: 5})
		writeErr = writer.doDBWrite()
		Expect(writeErr).Should(Succeed())

		writer.lock.Lock()
		pending := len(writer.pendingEntries)
		warned := writer.rollupWarned
		writer.lock.Unlock()
		Expect(pending).Should(Equal(1))
		Expect(warned).Should(BeTrue())
	})

	It("does not drop the pending queue when keepPending is asked to rewind nothing", func() {
		writer := &DatabaseWriter{pendingEntries: []*logEntry{{}, {}}}
		writer.keepPending(0)
		Expect(writer.pendingEntries).Should(HaveLen(2))
		writer.keepPending(1)
		Expect(writer.pendingEntries).Should(HaveLen(1))
	})

	It("serves aligned count, chart and top lists from rollups after raw rows are gone", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "history.db")
		writer, writeErr := NewDatabaseWriter(ctx, config.QueryLogTypeSqlite, dbPath, 7*24*time.Hour, time.Hour)
		Expect(writeErr).Should(Succeed())
		sqlDB, writeErr := writer.db.DB()
		Expect(writeErr).Should(Succeed())
		DeferCleanup(sqlDB.Close)

		base := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
		entries := []*LogEntry{
			{Start: base.Add(5 * time.Minute), QuestionName: "example.com", ClientIP: "192.168.1.10", ClientNames: []string{"alice-laptop"}, ResponseType: "RESOLVED", QuestionType: "A"},
			{Start: base.Add(10 * time.Minute), QuestionName: "ad.doubleclick.net", ClientIP: "192.168.1.10", ClientNames: []string{"alice-laptop"}, ResponseType: "BLOCKED", QuestionType: "A"},
			{Start: base.Add(15 * time.Minute), QuestionName: "example.com", ClientIP: "192.168.1.20", ResponseType: "CACHED", QuestionType: "AAAA"},
			{Start: base.Add(70 * time.Minute), QuestionName: "github.com", ClientIP: "192.168.1.20", ResponseType: "RESOLVED", QuestionType: "A"},
		}
		for _, entry := range entries {
			writer.Write(entry)
		}
		Expect(writer.doDBWrite()).Should(Succeed())

		reader, writeErr := NewDBReader(ctx, config.QueryLogTypeSqlite, dbPath)
		Expect(writeErr).Should(Succeed())
		DeferCleanup(reader.Close)
		Eventually(func() int64 {
			reader.rollupMu.Lock()
			defer reader.rollupMu.Unlock()

			return reader.coveredCounter + reader.coveredHour
		}, "20s").Should(Equal(int64(0)))

		filter := &Filter{From: base, To: base.Add(2 * time.Hour)}
		total, writeErr := reader.Count(filter)
		Expect(writeErr).Should(Succeed())
		Expect(total).Should(Equal(int64(4)))

		buckets, writeErr := reader.OverTime(filter, time.Hour)
		Expect(writeErr).Should(Succeed())
		Expect(buckets).Should(HaveLen(3))
		Expect(buckets[0].Total).Should(Equal(3))
		Expect(buckets[0].Blocked).Should(Equal(1))
		Expect(buckets[0].Cached).Should(Equal(1))
		Expect(buckets[1].Total).Should(Equal(1))
		Expect(buckets[2].Total).Should(Equal(0))

		topDomains, distinct, writeErr := reader.Top(filter, false, false, 1, 0)
		Expect(writeErr).Should(Succeed())
		Expect(topDomains).Should(HaveLen(1))
		Expect(topDomains[0].Name).Should(Equal("example.com"))
		Expect(topDomains[0].Total).Should(Equal(2))
		Expect(distinct).Should(Equal(3))

		Expect(writer.db.Exec("DELETE FROM log_entries").Error).Should(Succeed())

		total, writeErr = reader.Count(filter)
		Expect(writeErr).Should(Succeed())
		Expect(total).Should(Equal(int64(4)))
		topDomains, distinct, writeErr = reader.Top(filter, false, false, 10, 0)
		Expect(writeErr).Should(Succeed())
		Expect(distinct).Should(Equal(3))
		Expect(topDomains).Should(HaveLen(3))

		unaligned := &Filter{From: base, To: base.Add(2*time.Hour - time.Second)}
		rawTotal, writeErr := reader.Count(unaligned)
		Expect(writeErr).Should(Succeed())
		Expect(rawTotal).Should(Equal(int64(0)))

		var misaligned int64
		writeErr = reader.db.Raw(
			"SELECT COUNT(*) FROM "+hourTableName+" WHERE bucket_sec % ? != 0", topHourSec,
		).Row().Scan(&misaligned)
		Expect(writeErr).Should(Succeed())
		Expect(misaligned).Should(Equal(int64(0)))
	})

	It("answers a recent one-hour top list from 5 minute buckets", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "recent.db")
		writer, writeErr := NewDatabaseWriter(ctx, config.QueryLogTypeSqlite, dbPath, 24*time.Hour, time.Hour)
		Expect(writeErr).Should(Succeed())
		sqlDB, writeErr := writer.db.DB()
		Expect(writeErr).Should(Succeed())
		DeferCleanup(sqlDB.Close)

		end := alignDown(time.Now().UTC().Unix(), topRecentSec)
		start := end - topHourSec
		writer.Write(&LogEntry{Start: time.Unix(start+60, 0).UTC(), QuestionName: "recent.example", ClientIP: "10.0.0.8", ResponseType: "RESOLVED"})
		writer.Write(&LogEntry{Start: time.Unix(start+90, 0).UTC(), QuestionName: "recent.example", ClientIP: "10.0.0.8", ResponseType: "BLOCKED"})
		writer.Write(&LogEntry{Start: time.Unix(end-60, 0).UTC(), QuestionName: "other.example", ClientIP: "10.0.0.9", ResponseType: "CACHED"})
		Expect(writer.doDBWrite()).Should(Succeed())

		reader, writeErr := NewDBReader(ctx, config.QueryLogTypeSqlite, dbPath)
		Expect(writeErr).Should(Succeed())
		DeferCleanup(reader.Close)

		filter := &Filter{From: time.Unix(start, 0).UTC(), To: time.Unix(end, 0).UTC()}
		items, distinct, writeErr := reader.Top(filter, false, false, 10, 0)
		Expect(writeErr).Should(Succeed())
		Expect(distinct).Should(Equal(2))
		Expect(items[0].Name).Should(Equal("recent.example"))
		Expect(items[0].Total).Should(Equal(2))
		Expect(items[0].Blocked).Should(Equal(1))
	})

	It("keeps timescale log rows while deleting expired rollup buckets", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "timescale.db")
		writer, writeErr := NewDatabaseWriter(ctx, config.QueryLogTypeSqlite, dbPath, 24*time.Hour, time.Hour)
		Expect(writeErr).Should(Succeed())
		sqlDB, writeErr := writer.db.DB()
		Expect(writeErr).Should(Succeed())
		DeferCleanup(sqlDB.Close)

		writer.Write(&LogEntry{Start: time.Now(), QuestionName: "now.example", ResponseType: "RESOLVED"})
		writer.Write(&LogEntry{Start: time.Now().AddDate(0, 0, -2), QuestionName: "old.example", ResponseType: "BLOCKED"})
		Expect(writer.doDBWrite()).Should(Succeed())

		writer.dbType = config.QueryLogTypeTimescale
		writer.CleanUp()

		Eventually(countLogEntries, "5s").WithArguments(writer.db).Should(BeNumerically("==", 2))
		cutoff := alignDown(time.Now().Add(-24*time.Hour).Unix(), topHourSec)
		var oldTotal int64
		writeErr = writer.db.Raw(
			"SELECT COALESCE(SUM(total), 0) FROM "+hourTableName+" WHERE bucket_sec < ?", cutoff,
		).Row().Scan(&oldTotal)
		Expect(writeErr).Should(Succeed())
		Expect(oldTotal).Should(Equal(int64(0)))
	})

	It("uses conflict upserts for a timescale-marked sqlite writer", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "conflict.db")
		writer, writeErr := NewDatabaseWriter(ctx, config.QueryLogTypeSqlite, dbPath, time.Hour, time.Hour)
		Expect(writeErr).Should(Succeed())
		sqlDB, writeErr := writer.db.DB()
		Expect(writeErr).Should(Succeed())
		DeferCleanup(sqlDB.Close)

		writer.dbType = config.QueryLogTypeTimescale
		writer.Write(&LogEntry{Start: time.Now(), QuestionName: "a.example", ResponseType: "RESOLVED"})
		writer.Write(&LogEntry{Start: time.Now(), QuestionName: "a.example", ResponseType: "BLOCKED"})
		Expect(writer.doDBWrite()).Should(Succeed())

		var total int64
		writeErr = writer.db.Raw(
			"SELECT COALESCE(SUM(total), 0) FROM " + counterTableName,
		).Row().Scan(&total)
		Expect(writeErr).Should(Succeed())
		Expect(total).Should(Equal(int64(2)))
	})

	It("stores dashboard counters in less space than the raw log", func() {
		dbPath := filepath.Join(GinkgoT().TempDir(), "size.db")
		writer, writeErr := NewDatabaseWriter(ctx, config.QueryLogTypeSqlite, dbPath, 7*24*time.Hour, time.Hour)
		Expect(writeErr).Should(Succeed())
		sqlDB, writeErr := writer.db.DB()
		Expect(writeErr).Should(Succeed())
		DeferCleanup(sqlDB.Close)

		base := time.Now().UTC().Add(-6 * time.Hour)
		const rows = 20000
		for i := range rows {
			name := "hot.example"
			if i%10 == 0 {
				name = fmt.Sprintf("rare-%d.example", i%500)
			} else if i%3 == 0 {
				name = fmt.Sprintf("mid-%d.example", i%30)
			}
			writer.Write(&LogEntry{
				Start:        base.Add(time.Duration(i) * time.Second),
				QuestionName: name,
				ClientIP:     fmt.Sprintf("10.0.%d.%d", (i/20)%20, i%20),
				ClientNames:  []string{fmt.Sprintf("client-%d", i%40)},
				ResponseType: []string{"RESOLVED", "BLOCKED", "CACHED"}[i%3],
				Answer:       "1.2.3.4",
				ResponseCode: "NOERROR",
				QuestionType: "A",
			})
		}
		Expect(writer.doDBWrite()).Should(Succeed())

		before, writeErr := databaseBytes(writer.db)
		Expect(writeErr).Should(Succeed())
		Expect(writer.db.Exec("DROP TABLE " + recentTableName).Error).Should(Succeed())
		withoutRecent, writeErr := databaseBytes(writer.db)
		Expect(writeErr).Should(Succeed())
		Expect(writer.db.Exec("DROP TABLE " + hourTableName).Error).Should(Succeed())
		countersOnly, writeErr := databaseBytes(writer.db)
		Expect(writeErr).Should(Succeed())
		Expect(writer.db.Exec("DROP TABLE " + counterTableName).Error).Should(Succeed())
		raw, writeErr := databaseBytes(writer.db)
		Expect(writeErr).Should(Succeed())

		counters := countersOnly - raw
		hours := withoutRecent - countersOnly
		steady := counters + hours
		recent := before - withoutRecent
		fmt.Fprintf(GinkgoWriter, "storage bytes raw=%d counters=%d top_hours=%d steady_rollup=%d recent_5m=%d with_recent=%d\n",
			raw, counters, hours, steady, recent, before)
		Expect(raw).Should(BeNumerically(">", 0))
		Expect(steady).Should(BeNumerically(">", 0))
		Expect(steady).Should(BeNumerically("<", raw))
	})
})

func databaseBytes(db *gorm.DB) (int64, error) {
	if err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return 0, err
	}
	if err := db.Exec("VACUUM").Error; err != nil {
		return 0, err
	}

	var pages int64
	var pageSize int64
	if err := db.Raw("PRAGMA page_count").Row().Scan(&pages); err != nil {
		return 0, err
	}
	if err := db.Raw("PRAGMA page_size").Row().Scan(&pageSize); err != nil {
		return 0, err
	}

	return pages * pageSize, nil
}
