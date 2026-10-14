package stats

import (
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Store", func() {
	var (
		store *Store
		path  string
	)

	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "stats.db")

		var err error

		store, err = OpenStore(path)
		Expect(err).Should(Succeed())
	})

	AfterEach(func() {
		Expect(store.Close()).Should(Succeed())
	})

	mkBucket := func(hour time.Time) *bucket {
		b := newBucket(hour)
		b.dropped = 2
		b.errors = 1
		b.durationSumMs = 300
		b.byResponseType["CACHED"] = 5
		b.byResponseType["BLOCKED"] = 3
		b.byQueryType["A"] = 8
		b.byResponseCode["NOERROR"] = 8
		b.domains["a.com"] = 4
		b.domains["b.com"] = 4
		b.blockedDomains["evil.com"] = 3
		b.clients["c1"] = 8

		return b
	}

	It("round-trips buckets through save and load", func() {
		h1 := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
		h2 := time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC)

		Expect(store.SaveBucket(mkBucket(h1))).Should(Succeed())
		Expect(store.SaveBucket(mkBucket(h2))).Should(Succeed())

		buckets, err := store.LoadSince(h1.Add(-time.Hour))
		Expect(err).Should(Succeed())
		Expect(buckets).Should(HaveLen(2))

		Expect(buckets[0].hourStart).Should(Equal(h1))
		Expect(buckets[0].dropped).Should(Equal(2))
		Expect(buckets[0].errors).Should(Equal(1))
		Expect(buckets[0].durationSumMs).Should(Equal(int64(300)))
		Expect(buckets[0].byResponseType).Should(Equal(map[string]int{"CACHED": 5, "BLOCKED": 3}))
		Expect(buckets[0].byQueryType).Should(Equal(map[string]int{"A": 8}))
		Expect(buckets[0].byResponseCode).Should(Equal(map[string]int{"NOERROR": 8}))
		Expect(buckets[0].domains).Should(Equal(map[string]int{"a.com": 4, "b.com": 4}))
		Expect(buckets[0].blockedDomains).Should(Equal(map[string]int{"evil.com": 3}))
		Expect(buckets[0].clients).Should(Equal(map[string]int{"c1": 8}))
	})

	It("upserts the same hour bucket", func() {
		h := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

		Expect(store.SaveBucket(mkBucket(h))).Should(Succeed())

		b2 := mkBucket(h)
		b2.byResponseType["CACHED"] = 42
		Expect(store.SaveBucket(b2)).Should(Succeed())

		buckets, err := store.LoadSince(h)
		Expect(err).Should(Succeed())
		Expect(buckets).Should(HaveLen(1))
		Expect(buckets[0].byResponseType["CACHED"]).Should(Equal(42))
	})

	It("respects the cutoff on load and deletes before it", func() {
		old := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
		recent := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

		Expect(store.SaveBucket(mkBucket(old))).Should(Succeed())
		Expect(store.SaveBucket(mkBucket(recent))).Should(Succeed())

		Expect(store.DeleteBefore(time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC))).Should(Succeed())

		buckets, err := store.LoadSince(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
		Expect(err).Should(Succeed())
		Expect(buckets).Should(HaveLen(1))
		Expect(buckets[0].hourStart).Should(Equal(recent))
	})
})

var _ = Describe("Collector persistence", func() {
	It("rehydrates buckets from the store on creation", func() {
		path := filepath.Join(GinkgoT().TempDir(), "stats.db")

		store, err := OpenStore(path)
		Expect(err).Should(Succeed())

		fixedNow := time.Date(2026, 10, 14, 12, 30, 0, 0, time.UTC)
		sut := newCollectorWithClockAndStore(func() time.Time { return fixedNow }, store)

		sut.Record(Sample{Disposition: DispositionAnswered, RType: "CACHED",
			QType: "A", RCode: "NOERROR", Domain: "a.com", Client: "c1", DurationMs: 10})
		sut.Record(Sample{Disposition: DispositionAnswered, RType: "BLOCKED",
			QType: "A", RCode: "NOERROR", Domain: "evil.com", Client: "c1", DurationMs: 5})
		sut.Flush()
		Expect(store.Close()).Should(Succeed())

		// New collector on the same database: stats must survive the "restart".
		store2, err := OpenStore(path)
		Expect(err).Should(Succeed())

		restored := newCollectorWithClockAndStore(func() time.Time { return fixedNow }, store2)
		snap := restored.Snapshot()

		Expect(snap.Summary.Queries).Should(Equal(2))
		Expect(snap.Summary.Cached).Should(Equal(1))
		Expect(snap.Summary.Blocked).Should(Equal(1))
		Expect(snap.TopDomains).Should(ContainElement(NameCount{Name: "a.com", Count: 1}))
		Expect(snap.TopBlockedDomains).Should(ContainElement(NameCount{Name: "evil.com", Count: 1}))
		Expect(snap.TopClients).Should(ContainElement(NameCount{Name: "c1", Count: 2}))

		Expect(store2.Close()).Should(Succeed())
	})

	It("persists closed buckets on hour rollover", func() {
		path := filepath.Join(GinkgoT().TempDir(), "stats.db")

		store, err := OpenStore(path)
		Expect(err).Should(Succeed())

		now := time.Date(2026, 10, 14, 12, 30, 0, 0, time.UTC)
		sut := newCollectorWithClockAndStore(func() time.Time { return now }, store)

		sut.Record(Sample{Disposition: DispositionAnswered, RType: "CACHED",
			QType: "A", RCode: "NOERROR", Domain: "a.com", Client: "c1", DurationMs: 10})

		// Move into the next hour: the 12:00 bucket must be saved on rollover.
		now = time.Date(2026, 10, 14, 13, 5, 0, 0, time.UTC)
		sut.Record(Sample{Disposition: DispositionAnswered, RType: "CACHED",
			QType: "A", RCode: "NOERROR", Domain: "b.com", Client: "c1", DurationMs: 10})

		buckets, err := store.LoadSince(time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC))
		Expect(err).Should(Succeed())
		Expect(buckets).Should(HaveLen(1))
		Expect(buckets[0].hourStart).Should(Equal(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)))
		Expect(buckets[0].domains).Should(Equal(map[string]int{"a.com": 1}))

		Expect(store.Close()).Should(Succeed())
	})
})
