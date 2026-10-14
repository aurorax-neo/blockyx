package querylog_test

import (
	"os"
	"path/filepath"
	"time"

	"github.com/0xERR0R/blocky/querylog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Reader", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()

		// tab-separated, no header; columns per createQueryLogRow.
		rows := "2026-10-14 10:00:01\t192.168.1.10\tlaptop\t12\tCACHED\twww.example.com.\t1.2.3.4\tNOERROR\tCACHED\tA\tblockyx\n" +
			"2026-10-14 10:00:02\t192.168.1.11\tphone\t5\tBLOCKED (ads)\tads.example.com.\t\tNOERROR\tBLOCKED\tA\tblockyx\n" +
			"2026-10-14 10:00:03\t192.168.1.10\tlaptop\t30\tFORWARDED\twww.example.org.\t5.6.7.8\tNOERROR\tFORWARDED\tAAAA\tblockyx\n"

		Expect(os.WriteFile(filepath.Join(dir, "2026-10-14_ALL.log"), []byte(rows), 0o600)).Should(Succeed())
	})

	It("parses entries and matches filters", func() {
		entries, err := querylog.Collect(dir, &querylog.Filter{})
		Expect(err).Should(Succeed())
		Expect(entries).Should(HaveLen(3))

		Expect(entries[0].QuestionName).Should(Equal("www.example.com."))
		Expect(entries[0].ClientName).Should(Equal("laptop"))
		Expect(entries[0].Cached()).Should(BeTrue())
		Expect(entries[1].Blocked()).Should(BeTrue())

		blocked, err := querylog.Collect(dir, &querylog.Filter{ResponseType: "BLOCKED"})
		Expect(err).Should(Succeed())
		Expect(blocked).Should(HaveLen(1))

		byClient, err := querylog.Collect(dir, &querylog.Filter{Client: "phone"})
		Expect(err).Should(Succeed())
		Expect(byClient).Should(HaveLen(1))

		bySearch, err := querylog.Collect(dir, &querylog.Filter{Search: "example.org"})
		Expect(err).Should(Succeed())
		Expect(bySearch).Should(HaveLen(1))

		byTime, err := querylog.Collect(dir, &querylog.Filter{
			From: time.Date(2026, 10, 14, 10, 0, 2, 0, time.Local),
			To:   time.Date(2026, 10, 14, 10, 0, 2, 0, time.Local),
		})
		Expect(err).Should(Succeed())
		Expect(byTime).Should(HaveLen(1))
		Expect(byTime[0].QuestionName).Should(Equal("ads.example.com."))
	})

	It("ignores files outside the range and malformed rows", func() {
		bad := "not a row\n" +
			"2026-10-13 09:00:00\t192.168.1.99\told\t1\tCACHED\told.example.com.\t9.9.9.9\tNOERROR\tCACHED\tA\tblockyx\n"

		Expect(os.WriteFile(filepath.Join(dir, "2026-10-13_ALL.log"), []byte(bad), 0o600)).Should(Succeed())

		entries, err := querylog.Collect(dir, &querylog.Filter{
			From: time.Date(2026, 10, 14, 0, 0, 0, 0, time.Local),
		})
		Expect(err).Should(Succeed())
		Expect(entries).Should(HaveLen(3))
	})
})
