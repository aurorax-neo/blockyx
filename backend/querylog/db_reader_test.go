package querylog

import (
	"context"
	"path/filepath"
	"time"

	"github.com/0xERR0R/blocky/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DBReader Aggregations", func() {
	var (
		writer *DatabaseWriter
		reader *DBReader
		ctx    context.Context
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)

		tempDir := GinkgoT().TempDir()
		dbFile := filepath.Join(tempDir, "test.db")

		var err error
		writer, err = NewDatabaseWriter(ctx, config.QueryLogTypeSqlite, dbFile, 7*24*time.Hour, time.Millisecond)
		Expect(err).Should(Succeed())

		reader, err = NewDBReader(ctx, config.QueryLogTypeSqlite, dbFile)
		Expect(err).Should(Succeed())
		DeferCleanup(reader.Close)
	})

	It("aggregates OverTime, TopDomains, TopClients and QueryTypes correctly", func() {
		baseTime := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)

		entries := []*LogEntry{
			{Start: baseTime.Add(5 * time.Minute), QuestionName: "example.com", ClientIP: "192.168.1.10", ClientNames: []string{"alice-laptop"}, ResponseType: "RESOLVED", QuestionType: "A", DurationMs: 10},
			{Start: baseTime.Add(10 * time.Minute), QuestionName: "ad.doubleclick.net", ClientIP: "192.168.1.10", ClientNames: []string{"alice-laptop"}, ResponseType: "BLOCKED", ResponseReason: "BLOCKED (ads)", QuestionType: "A", DurationMs: 1},
			{Start: baseTime.Add(15 * time.Minute), QuestionName: "example.com", ClientIP: "192.168.1.20", ClientNames: nil, ResponseType: "CACHED", QuestionType: "AAAA", DurationMs: 0},
			{Start: baseTime.Add(70 * time.Minute), QuestionName: "github.com", ClientIP: "192.168.1.20", ClientNames: nil, ResponseType: "RESOLVED", QuestionType: "A", DurationMs: 25},
		}

		for _, e := range entries {
			writer.Write(e)
		}
		Expect(writer.doDBWrite()).Should(Succeed())

		// Test OverTime: 10:00 to 11:59:59 (2 hourly buckets)
		filter := &Filter{
			From: baseTime,
			To:   baseTime.Add(2*time.Hour - time.Second),
		}
		buckets, err := reader.OverTime(filter, time.Hour)
		Expect(err).Should(Succeed())
		Expect(buckets).Should(HaveLen(2))
		Expect(buckets[0].Total).Should(Equal(3))
		Expect(buckets[0].Blocked).Should(Equal(1))
		Expect(buckets[0].Cached).Should(Equal(1))
		Expect(buckets[1].Total).Should(Equal(1))
		Expect(buckets[1].Blocked).Should(Equal(0))
		Expect(buckets[1].Cached).Should(Equal(0))

		// Test Top Domains
		topDomains, totalCount, err := reader.Top(filter, false, false, 10, 0)
		Expect(err).Should(Succeed())
		Expect(totalCount).Should(Equal(3))
		Expect(topDomains).Should(HaveLen(3))
		Expect(topDomains[0].Name).Should(Equal("example.com"))
		Expect(topDomains[0].Total).Should(Equal(2))

		// Test Top Clients
		topClients, clientCount, err := reader.Top(filter, true, false, 10, 0)
		Expect(err).Should(Succeed())
		Expect(clientCount).Should(Equal(2))
		Expect(topClients).Should(HaveLen(2))
		// alice-laptop (2) and 192.168.1.20 (2)
		Expect(topClients[0].Total).Should(Equal(2))

		// Test QueryTypes
		qtypes, err := reader.QueryTypes(filter)
		Expect(err).Should(Succeed())
		Expect(qtypes).Should(HaveLen(2))
		Expect(qtypes[0].Type).Should(Equal("A"))
		Expect(qtypes[0].Count).Should(Equal(3))
		Expect(qtypes[1].Type).Should(Equal("AAAA"))
		Expect(qtypes[1].Count).Should(Equal(1))

		// Test Search
		suggestions, err := reader.Search(filter, false, "examp", 10)
		Expect(err).Should(Succeed())
		Expect(suggestions).Should(HaveLen(1))
		Expect(suggestions[0].Name).Should(Equal("example.com"))
		Expect(suggestions[0].Count).Should(Equal(2))
	})
})
