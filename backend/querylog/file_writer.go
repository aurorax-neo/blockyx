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
	"time"

	"github.com/0xERR0R/blocky/log"
	"github.com/0xERR0R/blocky/util"
	"github.com/sirupsen/logrus"
)

const (
	loggerPrefixFileWriter = "fileQueryLogWriter"
	filePermission         = 0o666
)

var validFilePattern = regexp.MustCompile("[^a-zA-Z0-9-_]+")

type FileWriter struct {
	target    string
	perClient bool
	retention time.Duration
	rotation  time.Duration
}

func NewCSVWriter(
	target string,
	perClient bool,
	retention time.Duration,
	rotation time.Duration,
) (*FileWriter, error) {
	if _, err := os.Stat(target); target != "" && err != nil && os.IsNotExist(err) {
		return nil, fmt.Errorf("query log directory '%s' does not exist or is not writable", target)
	}

	if rotation == 0 {
		rotation = 24 * time.Hour
	}

	return &FileWriter{
		target:    target,
		perClient: perClient,
		retention: retention,
		rotation:  rotation,
	}, nil
}

// fileName returns the log file name for the given entry, honoring the
// configured rotation granularity. The leading date part is always
// 2006-01-02 so retention cleanup keeps working across rotations.
func (d *FileWriter) fileName(entry *LogEntry) string {
	var datePart string

	switch {
	case d.rotation >= 24*time.Hour:
		datePart = entry.Start.Format("2006-01-02")
	case d.rotation >= time.Hour:
		datePart = entry.Start.Format("2006-01-02_15")
	default:
		datePart = entry.Start.Format("2006-01-02_15-04")
	}

	var clientPrefix string

	if d.perClient {
		clientPrefix = strings.Join(entry.ClientNames, "-")
	} else {
		clientPrefix = "ALL"
	}

	return fmt.Sprintf("%s_%s.log", datePart, escape(clientPrefix))
}

func (d *FileWriter) Write(entry *LogEntry) {
	writePath := filepath.Join(d.target, d.fileName(entry))

	file, err := os.OpenFile(writePath, os.O_APPEND|os.O_CREATE|os.O_RDWR, filePermission)

	util.LogOnErrorWithEntry(log.PrefixedLog(loggerPrefixFileWriter).WithField("file_name", writePath),
		"can't create/open file", err)

	if err == nil {
		defer file.Close()
		writer := createCsvWriter(file)

		err := writer.Write(createQueryLogRow(entry))
		util.LogOnErrorWithEntry(log.PrefixedLog(loggerPrefixFileWriter).WithField("file_name", writePath),
			"can't write to file", err)
		writer.Flush()
	}
}

// CleanUp deletes old log files
func (d *FileWriter) CleanUp() {
	logger := log.PrefixedLog(loggerPrefixFileWriter)

	logger.Trace("starting clean up")

	files, err := os.ReadDir(d.target)

	util.LogOnErrorWithEntry(logger.WithField("target", d.target), "can't list log directory: ", err)

	// search for log files, which names starts with date
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".log") && len(f.Name()) > 10 {
			t, err := time.ParseInLocation("2006-01-02", f.Name()[:10], time.Local)
			if err == nil {
				age := time.Since(t)
				if d.retention > 0 && age > d.retention {
					logger.WithFields(logrus.Fields{
						"file":      f.Name(),
						"age":       age.String(),
						"retention": d.retention.String(),
					}).Info("existing log file is older than retention time and will be deleted")

					err := os.Remove(filepath.Join(d.target, f.Name()))
					util.LogOnErrorWithEntry(logger.WithField("file", f.Name()), "can't remove file: ", err)
				}
			}
		}
	}
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
