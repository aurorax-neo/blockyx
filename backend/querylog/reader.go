package querylog

// Reader for blocky's own CSV query log files, backing the embedded web UI's
// log viewer. Only the file-based (csv) log type is supported; other types
// (mysql, postgresql, ...) are managed outside of the single binary.

import (
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CSV column layout, see createQueryLogRow in file_writer.go (tab separated).
const (
	colStart = iota
	colClientIP
	colClientNames
	colDurationMs
	colReason
	colQuestionName
	colAnswer
	colResponseCode
	colResponseType
	colQuestionType
	colCount
)

// Record is one parsed query log row.
type Record struct {
	Time         time.Time
	ClientIP     string
	ClientName   string
	DurationMs   int64
	Reason       string
	QuestionName string
	Answer       string
	ResponseCode string
	ResponseType string
	QuestionType string
}

// Blocked reports whether the query was blocked.
func (e *Record) Blocked() bool {
	return e.ResponseType == "BLOCKED" || strings.HasPrefix(e.Reason, "BLOCKED")
}

// Cached reports whether the query was answered from cache.
func (e *Record) Cached() bool {
	return e.ResponseType == "CACHED"
}

// Filter selects log entries.
type Filter struct {
	From, To     time.Time
	Search       string // substring over question name, client ip/name, answer
	Domain       string // exact question name match (case-insensitive)
	Client       string // exact client ip or name match (case-insensitive)
	QuestionType string // e.g. "A"
	ResponseType string // e.g. "BLOCKED"
}

// Matches reports whether the entry satisfies the filter.
func (f *Filter) Matches(e *Record) bool {
	if !f.From.IsZero() && e.Time.Before(f.From) {
		return false
	}

	if !f.To.IsZero() && e.Time.After(f.To) {
		return false
	}

	if f.Domain != "" && !strings.EqualFold(e.QuestionName, f.Domain) {
		return false
	}

	if f.Client != "" &&
		!strings.EqualFold(e.ClientIP, f.Client) &&
		!strings.EqualFold(e.ClientName, f.Client) {
		return false
	}

	if f.QuestionType != "" && !strings.EqualFold(e.QuestionType, f.QuestionType) {
		return false
	}

	if f.ResponseType != "" && !strings.EqualFold(e.ResponseType, f.ResponseType) {
		return false
	}

	if f.Search != "" {
		s := strings.ToLower(f.Search)

		if !strings.Contains(strings.ToLower(e.QuestionName), s) &&
			!strings.Contains(strings.ToLower(e.ClientIP), s) &&
			!strings.Contains(strings.ToLower(e.ClientName), s) &&
			!strings.Contains(strings.ToLower(e.Answer), s) {
			return false
		}
	}

	return true
}

// logFiles returns the csv log files in dir overlapping [from, to].
// File names start with the date (2006-01-02) for all rotation modes
// (daily/hourly/minutely).
func logFiles(dir string, from, to time.Time) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var files []string

	// Widen by one day: a file's date prefix only tells the day it belongs to.
	low := from.Add(-24 * time.Hour)
	if from.IsZero() {
		low = time.Time{}
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".log") || len(name) < 10 {
			continue
		}

		day, err := time.ParseInLocation("2006-01-02", name[:10], time.Local)
		if err != nil {
			continue
		}

		if !low.IsZero() && day.Before(startOfDay(low)) {
			continue
		}

		if !to.IsZero() && day.After(startOfDay(to)) {
			continue
		}

		files = append(files, filepath.Join(dir, name))
	}

	// Oldest first so merged results stay chronological.
	sort.Strings(files)

	return files
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()

	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func parseEntry(record []string) *Record {
	if len(record) < colCount {
		return nil
	}

	ts, err := time.ParseInLocation("2006-01-02 15:04:05", record[colStart], time.Local)
	if err != nil {
		return nil
	}

	durationMs, _ := strconv.ParseInt(record[colDurationMs], 10, 64)

	name := record[colClientNames]
	if i := strings.Index(name, ";"); i >= 0 {
		name = strings.TrimSpace(name[:i])
	}

	return &Record{
		Time:         ts,
		ClientIP:     record[colClientIP],
		ClientName:   name,
		DurationMs:   durationMs,
		Reason:       record[colReason],
		QuestionName: record[colQuestionName],
		Answer:       record[colAnswer],
		ResponseCode: record[colResponseCode],
		ResponseType: record[colResponseType],
		QuestionType: record[colQuestionType],
	}
}

// Scan visits every entry in the log files overlapping the filter's range.
// Entries arrive oldest first.
func Scan(dir string, filter *Filter, visit func(*Record) error) error {
	for _, path := range logFiles(dir, filter.From, filter.To) {
		if err := scanFile(path, filter, visit); err != nil {
			return err
		}
	}

	return nil
}

func scanFile(path string, filter *Filter, visit func(*Record) error) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	for {
		record, err := r.Read()
		if err == io.EOF {
			return nil
		}

		if err != nil {
			// Skip malformed rows, keep scanning.
			continue
		}

		e := parseEntry(record)
		if e == nil {
			continue
		}

		if !filter.Matches(e) {
			continue
		}

		if err := visit(e); err != nil {
			return err
		}
	}
}

// Collect gathers all matching entries (oldest first).
func Collect(dir string, filter *Filter) ([]*Record, error) {
	var out []*Record

	err := Scan(dir, filter, func(e *Record) error {
		out = append(out, e)

		return nil
	})

	return out, err
}
