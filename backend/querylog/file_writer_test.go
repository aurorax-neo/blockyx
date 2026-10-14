package querylog

import (
	"bufio"
	"encoding/csv"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/0xERR0R/blocky/helpertest"
	"github.com/0xERR0R/blocky/log"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FileWriter", func() {
	var (
		tmpDir *helpertest.TmpFolder
		err    error
		writer *FileWriter
	)

	JustBeforeEach(func() {
		tmpDir = helpertest.NewTmpFolder("fileWriter")
	})

	Describe("CSV writer", func() {
		When("target dir does not exist", func() {
			It("should return error", func() {
				_, err = NewCSVWriter("wrongdir", false, 0, 100*1024*1024)
				Expect(err).Should(HaveOccurred())
			})
		})
		When("New log entry was created", func() {
			It("should be logged in one file", func() {
				writer, err = NewCSVWriter(tmpDir.Path, false, 0, 100*1024*1024)

				Expect(err).Should(Succeed())

				By("entry for client 1", func() {
					writer.Write(&LogEntry{
						ClientNames: []string{"client1"},
						Start:       time.Now(),
						DurationMs:  20,
					})
				})

				By("entry for client 2", func() {
					writer.Write(&LogEntry{
						ClientNames: []string{"client2"},
						Start:       time.Now(),
						DurationMs:  20,
					})
				})

				Eventually(func(g Gomega) int {
					return len(readCsv(tmpDir.JoinPath(
						time.Now().Format("2006-01-02") + "_ALL_001.log")))
				}).Should(Equal(2))
			})

			It("should be logged in separate files per client", func() {
				writer, err = NewCSVWriter(tmpDir.Path, true, 0, 100*1024*1024)

				Expect(err).Should(Succeed())

				By("entry for client 1", func() {
					writer.Write(&LogEntry{
						ClientNames: []string{"client1"},
						Start:       time.Now(),
						DurationMs:  20,
					})
				})

				By("entry for client 2", func() {
					writer.Write(&LogEntry{
						ClientNames: []string{"client2"},
						Start:       time.Now(),
						DurationMs:  20,
					})
				})

				Eventually(func(g Gomega) int {
					return len(readCsv(tmpDir.JoinPath(
						time.Now().Format("2006-01-02") + "_client1_001.log")))
				}).Should(Equal(1))

				Eventually(func(g Gomega) int {
					return len(readCsv(tmpDir.JoinPath(
						time.Now().Format("2006-01-02") + "_client2_001.log")))
				}).Should(Equal(1))
			})
		})
		When("rotation size is exceeded", func() {
			It("should rotate to a new file", func() {
				// tiny rotation size to force rotation (one entry is ~35 bytes)
				writer, err = NewCSVWriter(tmpDir.Path, false, 0, 100)

				Expect(err).Should(Succeed())

				now := time.Now()
				for i := 0; i < 5; i++ {
					writer.Write(&LogEntry{
						ClientNames: []string{"client1"},
						Start:       now,
						DurationMs:  20,
					})
				}

				base := now.Format("2006-01-02") + "_ALL"
				Eventually(func(g Gomega) int {
					files, _ := os.ReadDir(tmpDir.Path)
					count := 0

					for _, f := range files {
						if len(f.Name()) > len(base) && f.Name()[:len(base)] == base {
							count++
						}
					}

					return count
				}).Should(BeNumerically(">", 1))
			})
		})
		When("rotation is empty", func() {
			It("should default to 100MB", func() {
				writer, err = NewCSVWriter(tmpDir.Path, false, 0, 0)

				Expect(err).Should(Succeed())

				writer.Write(&LogEntry{
					ClientNames: []string{"client1"},
					Start:       time.Now(),
					DurationMs:  20,
				})

				Eventually(func(g Gomega) int {
					return len(readCsv(tmpDir.JoinPath(
						time.Now().Format("2006-01-02") + "_ALL_001.log")))
				}).Should(Equal(1))
			})
		})
		When("Cleanup is called", func() {
			It("should delete old files", func() {
				writer, err = NewCSVWriter(tmpDir.Path, false, 24*time.Hour, 100*1024*1024)

				Expect(err).Should(Succeed())

				By("entry today", func() {
					writer.Write(&LogEntry{
						ClientNames: []string{"client1"},
						Start:       time.Now(),
						DurationMs:  20,
					})
				})
				By("entry 2 days ago", func() {
					writer.Write(&LogEntry{
						ClientNames: []string{"client1"},
						Start:       time.Now().AddDate(0, 0, -3),
						DurationMs:  20,
					})
				})

				Eventually(func(g Gomega) ([]fs.DirEntry, error) {
					return os.ReadDir(tmpDir.Path)
				}, "20s", "1s").Should(HaveLen(2))

				writer.CleanUp()

				Eventually(func(g Gomega) ([]fs.DirEntry, error) {
					return os.ReadDir(tmpDir.Path)
				}, "20s", "1s").Should(HaveLen(1))
			})

			It("should remove only expired entries within a file", func() {
				// retention 48h: entry from 3 days ago expires, entry from 1 day ago stays
				writer, err = NewCSVWriter(tmpDir.Path, false, 48*time.Hour, 100*1024*1024)

				Expect(err).Should(Succeed())

				now := time.Now()
				// Both entries land in files keyed by their own date; write them
				// directly into one file to test per-entry filtering.
				path := tmpDir.JoinPath(now.Format("2006-01-02") + "_ALL_001.log")
				f, err := os.Create(path)
				Expect(err).Should(Succeed())

				w := csv.NewWriter(f)
				w.Comma = '\t'

				oldTs := now.AddDate(0, 0, -3).Format("2006-01-02 15:04:05")
				freshTs := now.AddDate(0, 0, -1).Format("2006-01-02 15:04:05")

				Expect(w.Write([]string{oldTs, "1.1.1.1", "c1", "20", "ok", "example.com.", "", "NOERROR", "RESOLVED", "A", ""})).Should(Succeed())
				Expect(w.Write([]string{freshTs, "1.1.1.1", "c1", "20", "ok", "example.org.", "", "NOERROR", "RESOLVED", "A", ""})).Should(Succeed())
				w.Flush()
				Expect(f.Close()).Should(Succeed())

				writer.CleanUp()

				rows := readCsv(path)
				Expect(rows).Should(HaveLen(1))
				Expect(rows[0][5]).Should(Equal("example.org."))
			})
		})
	})
})

func readCsv(file string) [][]string {
	var result [][]string

	csvFile, err := os.Open(file)
	Expect(err).Should(Succeed())

	reader := csv.NewReader(bufio.NewReader(csvFile))
	reader.Comma = '\t'

	for {
		line, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			log.Log().Fatal("can't read line", err)
		}

		result = append(result, line)
	}

	return result
}
