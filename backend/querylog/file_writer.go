package querylog

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xERR0R/blocky/config"
	"github.com/0xERR0R/blocky/log"
	"github.com/0xERR0R/blocky/util"
	"github.com/sirupsen/logrus"
)

const (
	loggerPrefixFileWriter = "fileQueryLogWriter"
	filePermission         = 0o666
)

var validFilePattern = regexp.MustCompile("[^a-zA-Z0-9-_]+")

// seqPattern matches the trailing _NNN sequence in rotated file names.
var seqPattern = regexp.MustCompile(`_(\d{3})\.log$`)

type FileWriter struct {
	target    string
	perClient bool
	retention time.Duration
	rotation  uint64 // max bytes per file before rotating

	mu          sync.Mutex
	currentBase string
	currentSeq  int
	currentSize uint64
}

func NewCSVWriter(
	target string,
	perClient bool,
	retention time.Duration,
	rotation uint64,
) (*FileWriter, error) {
	if _, err := os.Stat(target); target != "" && err != nil && os.IsNotExist(err) {
		return nil, fmt.Errorf("query log directory '%s' does not exist or is not writable", target)
	}

	if rotation == 0 {
		rotation = 100 * config.Megabyte
	}

	return &FileWriter{
		target:    target,
		perClient: perClient,
		retention: retention,
		rotation:  rotation,
	}, nil
}

// baseName returns the date + client prefix for the given entry.
// The leading date part is always 2006-01-02 so retention cleanup keeps working.
func (d *FileWriter) baseName(entry *LogEntry) string {
	datePart := entry.Start.Format("2006-01-02")

	var clientPrefix string

	if d.perClient {
		clientPrefix = strings.Join(entry.ClientNames, "-")
	} else {
		clientPrefix = "ALL"
	}

	return fmt.Sprintf("%s_%s", datePart, escape(clientPrefix))
}

// fileName returns the full file name for the current sequence.
func (d *FileWriter) fileName(base string, seq int) string {
	return fmt.Sprintf("%s_%03d.log", base, seq)
}

// ensureCurrent locks and makes sure currentSeq/currentSize point at the right
// file for the given base, scanning the directory on base change (e.g. new day
// or restart) to continue the existing sequence.
func (d *FileWriter) ensureCurrent(base string) {
	if base == d.currentBase {
		return
	}

	d.currentBase = base
	d.currentSeq = 1
	d.currentSize = 0

	// Continue the sequence if files already exist (e.g. after restart)
	files, err := os.ReadDir(d.target)
	if err != nil {
		return
	}

	for _, f := range files {
		name := f.Name()
		if !strings.HasPrefix(name, base+"_") || !strings.HasSuffix(name, ".log") {
			continue
		}

		m := seqPattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}

		seq, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}

		if seq >= d.currentSeq {
			d.currentSeq = seq

			if info, err := f.Info(); err == nil {
				d.currentSize = uint64(info.Size())
			}
		}
	}
}

func (d *FileWriter) Write(entry *LogEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()

	base := d.baseName(entry)
	d.ensureCurrent(base)

	// Rotate when the current file reached the size limit
	if d.currentSize >= d.rotation {
		d.currentSeq++
		d.currentSize = 0
	}

	writePath := filepath.Join(d.target, d.fileName(base, d.currentSeq))

	file, err := os.OpenFile(writePath, os.O_APPEND|os.O_CREATE|os.O_RDWR, filePermission)

	util.LogOnErrorWithEntry(log.PrefixedLog(loggerPrefixFileWriter).WithField("file_name", writePath),
		"can't create/open file", err)

	if err != nil {
		return
	}

	defer file.Close()

	writer := createCsvWriter(file)

	row := createQueryLogRow(entry)
	if err := writer.Write(row); err != nil {
		util.LogOnErrorWithEntry(log.PrefixedLog(loggerPrefixFileWriter).WithField("file_name", writePath),
			"can't write to file", err)

		return
	}

	writer.Flush()

	// Track size: stat is cheap enough here and stays correct across restarts
	if info, err := file.Stat(); err == nil {
		d.currentSize = uint64(info.Size())
	}
}

// CleanUp deletes individual log entries older than the retention period.
// Each CSV file is rewritten without the expired entries; files where all
// entries expired are removed entirely.
func (d *FileWriter) CleanUp() {
	if d.retention <= 0 {
		return
	}

	logger := log.PrefixedLog(loggerPrefixFileWriter)

	logger.Trace("starting clean up")

	files, err := os.ReadDir(d.target)

	util.LogOnErrorWithEntry(logger.WithField("target", d.target), "can't list log directory: ", err)

	cutoff := time.Now().Add(-d.retention)

	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".log") {
			continue
		}

		d.cleanupFile(logger, f.Name(), cutoff)
	}
}

// cleanupFile removes expired entries from a single log file.
func (d *FileWriter) cleanupFile(logger *logrus.Entry, name string, cutoff time.Time) {
	path := filepath.Join(d.target, name)

	file, err := os.Open(path)
	if err != nil {
		util.LogOnErrorWithEntry(logger.WithField("file", name), "can't open log file for cleanup: ", err)

		return
	}

	reader := csv.NewReader(file)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1

	var kept [][]string

	expired := 0

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}

		if err != nil {
			util.LogOnErrorWithEntry(logger.WithField("file", name), "can't parse log file during cleanup: ", err)

			break
		}

		entry := parseEntry(record)
		if entry == nil {
			// Keep unparseable lines rather than dropping data silently.
			kept = append(kept, record)

			continue
		}

		if entry.Time.Before(cutoff) {
			expired++
		} else {
			kept = append(kept, record)
		}
	}

	_ = file.Close()

	if expired == 0 {
		return
	}

	if len(kept) == 0 {
		logger.WithFields(logrus.Fields{
			"file":    name,
			"expired": expired,
		}).Info("all log entries expired, removing file")

		if err := os.Remove(path); err != nil {
			util.LogOnErrorWithEntry(logger.WithField("file", name), "can't remove log file: ", err)
		}

		return
	}

	// Rewrite the file with only the fresh entries, atomically.
	tmp, err := os.CreateTemp(d.target, ".cleanup-*")
	if err != nil {
		util.LogOnErrorWithEntry(logger.WithField("file", name), "can't create temp file for cleanup: ", err)

		return
	}

	tmpName := tmp.Name()

	writer := csv.NewWriter(tmp)
	writer.Comma = '\t'

	writeErr := writer.WriteAll(kept)
	writer.Flush()

	closeErr := tmp.Close()

	if writeErr != nil || closeErr != nil {
		util.LogOnErrorWithEntry(logger.WithField("file", name), "can't rewrite log file during cleanup: ", writeErr)

		_ = os.Remove(tmpName)

		return
	}

	if err := os.Rename(tmpName, path); err != nil {
		util.LogOnErrorWithEntry(logger.WithField("file", name), "can't replace log file after cleanup: ", err)

		_ = os.Remove(tmpName)

		return
	}

	logger.WithFields(logrus.Fields{
		"file":    name,
		"expired": expired,
		"kept":    len(kept),
	}).Info("removed expired log entries")
}

func createQueryLogRow(logEntry *LogEntry) []string {
	return []string{
		logEntry.Start.Format("2006-01-02 15:04:05"),
		logEntry.ClientIP,
		strings.Join(logEntry.ClientNames, "; "),
		strconv.FormatInt(logEntry.DurationMs, 10),
		logEntry.ResponseReason,
		logEntry.QuestionName,
		logEntry.Answer,
		logEntry.ResponseCode,
		logEntry.ResponseType,
		logEntry.QuestionType,
		logEntry.BlockyInstance,
	}
}

func createCsvWriter(file io.Writer) *csv.Writer {
	writer := csv.NewWriter(file)
	writer.Comma = '\t'

	return writer
}

func escape(file string) string {
	return validFilePattern.ReplaceAllString(file, "_")
}
