package server

// Query log HTTP endpoints for the embedded web UI.
// They expose blocky's query logs under /api/querylog/*,
// backed by either CSV files or a database (sqlite/mysql/postgresql/timescale),
// protected by the same API key middleware as the rest of /api.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0xERR0R/blocky/api"
	"github.com/0xERR0R/blocky/config"
	"github.com/0xERR0R/blocky/querylog"
	"github.com/go-chi/chi/v5"
)

var errQueryLogUnsupported = errors.New("query log API requires csv, sqlite, mysql, postgresql or timescale log type")

// queryLogSource abstracts CSV vs database backends.
type queryLogSource interface {
	rows(filter *querylog.Filter, limit, offset int) ([]queryLogEntryJSON, bool, error)
	count(filter *querylog.Filter) (int64, error)
	overTime(filter *querylog.Filter, interval time.Duration) ([]overTimeBucket, error)
	top(filter *querylog.Filter, byClient, blockedOnly bool, limit, offset int) ([]topEntry, int, error)
	search(filter *querylog.Filter, byClient bool, q string, limit int) ([]searchSuggestion, error)
	queryTypes(filter *querylog.Filter) ([]queryTypeCount, error)
}

// queryLogHandler serves the query log endpoints.
type queryLogHandler struct {
	source queryLogSource
}

func newQueryLogHandler(cfg *config.Config) (*queryLogHandler, error) {
	switch cfg.QueryLog.Type {
	case config.QueryLogTypeCsv, config.QueryLogTypeCsvClient:
		return &queryLogHandler{source: &csvSource{dir: cfg.QueryLog.Target.Reveal()}}, nil
	case config.QueryLogTypeSqlite, config.QueryLogTypeMysql,
		config.QueryLogTypePostgresql, config.QueryLogTypeTimescale:
		db, err := querylog.NewDBReader(context.Background(), cfg.QueryLog.Type, cfg.QueryLog.Target.Reveal())
		if err != nil {
			return nil, err
		}

		return &queryLogHandler{source: &dbSource{reader: db}}, nil
	default:
		return nil, errQueryLogUnsupported
	}
}

func configureQueryLogHandler(router *chi.Mux, cfg *config.Config) {
	h, err := newQueryLogHandler(cfg)
	if err != nil {
		router.Route("/api/querylog", func(r chi.Router) {
			r.Use(api.APIKeyAuthMiddleware(cfg.API.APIKey.Reveal()))
			r.Get("/*", func(w http.ResponseWriter, _ *http.Request) {
				writeQueryLogError(w, http.StatusServiceUnavailable, err.Error())
			})
		})

		return
	}

	router.Route("/api/querylog", func(r chi.Router) {
		r.Use(api.APIKeyAuthMiddleware(cfg.API.APIKey.Reveal()))
		r.Get("/rows", h.rows)
		r.Get("/count", h.count)
		r.Get("/over-time", h.overTime)
		r.Get("/top", h.top)
		r.Get("/search", h.search)
		r.Get("/query-types", h.queryTypes)
	})
}

func writeQueryLogJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeQueryLogError(w http.ResponseWriter, status int, msg string) {
	writeQueryLogJSON(w, status, map[string]string{"error": msg})
}

// queryLogEntryJSON mirrors the web UI's Record shape.
type queryLogEntryJSON struct {
	RequestTs    string `json:"requestTs"`
	ClientIP     string `json:"clientIp"`
	ClientName   string `json:"clientName"`
	DurationMs   int64  `json:"durationMs"`
	Reason       string `json:"reason"`
	QuestionName string `json:"questionName"`
	Answer       string `json:"answer"`
	ResponseCode string `json:"responseCode"`
	ResponseType string `json:"responseType"`
	QuestionType string `json:"questionType"`
}

func toJSONEntry(e *querylog.Record) queryLogEntryJSON {
	return queryLogEntryJSON{
		RequestTs:    e.Time.Format(time.RFC3339),
		ClientIP:     e.ClientIP,
		ClientName:   e.ClientName,
		DurationMs:   e.DurationMs,
		Reason:       e.Reason,
		QuestionName: e.QuestionName,
		Answer:       e.Answer,
		ResponseCode: e.ResponseCode,
		ResponseType: e.ResponseType,
		QuestionType: e.QuestionType,
	}
}

type overTimeBucket struct {
	Time    string `json:"time"`
	Total   int    `json:"total"`
	Blocked int    `json:"blocked"`
	Cached  int    `json:"cached"`
}

type topEntry struct {
	Name    string `json:"name"`
	Total   int    `json:"total"`
	Blocked int    `json:"blocked"`
}

type searchSuggestion struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type queryTypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// csvSource implements queryLogSource over CSV files.
type csvSource struct {
	dir string
}

func (s *csvSource) rows(filter *querylog.Filter, limit, offset int) ([]queryLogEntryJSON, bool, error) {
	want := offset + limit + 1
	window := make([]queryLogEntryJSON, 0, want)
	total := 0

	scanErr := querylog.Scan(s.dir, filter, func(e *querylog.Record) error {
		total++

		if len(window) == want {
			window = window[1:]
		}

		window = append(window, toJSONEntry(e))

		return nil
	})
	if scanErr != nil {
		return nil, false, scanErr
	}

	reverseEntries(window)

	hasMore := total > offset+limit
	if len(window) > limit {
		window = window[:limit]
	}

	return window, hasMore, nil
}

func (s *csvSource) count(filter *querylog.Filter) (int64, error) {
	var total int64

	err := querylog.Scan(s.dir, filter, func(*querylog.Record) error {
		total++

		return nil
	})

	return total, err
}

func (s *csvSource) overTime(filter *querylog.Filter, interval time.Duration) ([]overTimeBucket, error) {
	buckets := make(map[string]*overTimeBucket)

	var order []string

	err := querylog.Scan(s.dir, filter, func(e *querylog.Record) error {
		key := e.Time.Truncate(interval).Format(time.RFC3339)

		b, ok := buckets[key]
		if !ok {
			b = &overTimeBucket{Time: key}
			buckets[key] = b
			order = append(order, key)
		}

		b.Total++

		if e.Blocked() {
			b.Blocked++
		}

		if e.Cached() {
			b.Cached++
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(order)

	items := make([]overTimeBucket, 0, len(order))
	for _, k := range order {
		items = append(items, *buckets[k])
	}

	return items, nil
}

func (s *csvSource) top(filter *querylog.Filter, byClient, blockedOnly bool, limit, offset int) ([]topEntry, int, error) {
	counts := make(map[string]*topEntry)

	err := querylog.Scan(s.dir, filter, func(e *querylog.Record) error {
		if blockedOnly && !e.Blocked() {
			return nil
		}

		name := e.QuestionName
		if byClient {
			name = e.ClientIP
			if e.ClientName != "" {
				name = e.ClientName
			}
		}

		if name == "" {
			return nil
		}

		t, ok := counts[name]
		if !ok {
			t = &topEntry{Name: name}
			counts[name] = t
		}

		t.Total++

		if e.Blocked() {
			t.Blocked++
		}

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	items := make([]topEntry, 0, len(counts))
	for _, t := range counts {
		items = append(items, *t)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Total != items[j].Total {
			return items[i].Total > items[j].Total
		}

		return items[i].Name < items[j].Name
	})

	totalCount := len(items)

	if offset > len(items) {
		offset = len(items)
	}

	items = items[offset:]

	if len(items) > limit {
		items = items[:limit]
	}

	return items, totalCount, nil
}

func (s *csvSource) search(filter *querylog.Filter, byClient bool, q string, limit int) ([]searchSuggestion, error) {
	q = strings.ToLower(q)

	counts := make(map[string]int)

	var order []string

	err := querylog.Scan(s.dir, filter, func(e *querylog.Record) error {
		name := e.QuestionName
		if byClient {
			name = e.ClientIP
			if e.ClientName != "" {
				name = e.ClientName
			}
		}

		if name == "" {
			return nil
		}

		if q != "" && !strings.Contains(strings.ToLower(name), q) {
			return nil
		}

		if _, ok := counts[name]; !ok {
			order = append(order, name)
		}

		counts[name]++

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(order, func(i, j int) bool {
		if counts[order[i]] != counts[order[j]] {
			return counts[order[i]] > counts[order[j]]
		}

		return order[i] < order[j]
	})

	if len(order) > limit {
		order = order[:limit]
	}

	items := make([]searchSuggestion, 0, len(order))
	for _, name := range order {
		items = append(items, searchSuggestion{Name: name, Count: counts[name]})
	}

	return items, nil
}

func (s *csvSource) queryTypes(filter *querylog.Filter) ([]queryTypeCount, error) {
	counts := make(map[string]int)

	err := querylog.Scan(s.dir, filter, func(e *querylog.Record) error {
		counts[e.QuestionType]++

		return nil
	})
	if err != nil {
		return nil, err
	}

	items := make([]queryTypeCount, 0, len(counts))
	for t, c := range counts {
		items = append(items, queryTypeCount{Type: t, Count: c})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Count > items[j].Count })

	return items, nil
}

// dbSource implements queryLogSource over a database backend.
type dbSource struct {
	reader *querylog.DBReader
}

func (s *dbSource) rows(filter *querylog.Filter, limit, offset int) ([]queryLogEntryJSON, bool, error) {
	records, err := s.reader.Rows(filter, limit+1, offset)
	if err != nil {
		return nil, false, err
	}

	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	items := make([]queryLogEntryJSON, 0, len(records))
	for _, e := range records {
		items = append(items, toJSONEntry(e))
	}

	return items, hasMore, nil
}

func (s *dbSource) count(filter *querylog.Filter) (int64, error) {
	return s.reader.Count(filter)
}

func (s *dbSource) overTime(filter *querylog.Filter, interval time.Duration) ([]overTimeBucket, error) {
	buckets, err := s.reader.OverTime(filter, interval)
	if err != nil {
		return nil, err
	}

	items := make([]overTimeBucket, 0, len(buckets))
	for _, b := range buckets {
		items = append(items, overTimeBucket{
			Time:    b.Time.Format(time.RFC3339),
			Total:   b.Total,
			Blocked: b.Blocked,
			Cached:  b.Cached,
		})
	}

	return items, nil
}

func (s *dbSource) top(filter *querylog.Filter, byClient, blockedOnly bool, limit, offset int) ([]topEntry, int, error) {
	entries, total, err := s.reader.Top(filter, byClient, blockedOnly, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	items := make([]topEntry, 0, len(entries))
	for _, e := range entries {
		items = append(items, topEntry{Name: e.Name, Total: e.Total, Blocked: e.Blocked})
	}

	return items, total, nil
}

func (s *dbSource) search(filter *querylog.Filter, byClient bool, q string, limit int) ([]searchSuggestion, error) {
	suggestions, err := s.reader.Search(filter, byClient, q, limit)
	if err != nil {
		return nil, err
	}

	items := make([]searchSuggestion, 0, len(suggestions))
	for _, sg := range suggestions {
		items = append(items, searchSuggestion{Name: sg.Name, Count: sg.Count})
	}

	return items, nil
}

func (s *dbSource) queryTypes(filter *querylog.Filter) ([]queryTypeCount, error) {
	counts, err := s.reader.QueryTypes(filter)
	if err != nil {
		return nil, err
	}

	items := make([]queryTypeCount, 0, len(counts))
	for _, c := range counts {
		items = append(items, queryTypeCount{Type: c.Type, Count: c.Count})
	}

	return items, nil
}

func reverseEntries(items []queryLogEntryJSON) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func parseQueryLogFilter(r *http.Request) (*querylog.Filter, error) {
	q := r.URL.Query()
	f := &querylog.Filter{
		Search:       q.Get("search"),
		Domain:       q.Get("domain"),
		Client:       q.Get("client"),
		QuestionType: q.Get("qtype"),
		ResponseType: q.Get("rtype"),
	}

	var err error

	if v := q.Get("from"); v != "" {
		if f.From, err = time.Parse(time.RFC3339, v); err != nil {
			return nil, err
		}
	}

	if v := q.Get("to"); v != "" {
		if f.To, err = time.Parse(time.RFC3339, v); err != nil {
			return nil, err
		}
	}

	return f, nil
}

func queryInt(r *http.Request, name string, def int) int {
	if v := r.URL.Query().Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}

	return def
}

// GET /api/querylog/rows — paginated log rows, newest first.
func (h *queryLogHandler) rows(w http.ResponseWriter, r *http.Request) {
	filter, err := parseQueryLogFilter(r)
	if err != nil {
		writeQueryLogError(w, http.StatusBadRequest, "invalid time range: "+err.Error())

		return
	}

	limit := queryInt(r, "limit", 10)
	offset := queryInt(r, "offset", 0)

	items, hasMore, err := h.source.rows(filter, limit, offset)
	if err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	writeQueryLogJSON(w, http.StatusOK, map[string]any{
		"items":   items,
		"hasMore": hasMore,
	})
}

// GET /api/querylog/count
func (h *queryLogHandler) count(w http.ResponseWriter, r *http.Request) {
	filter, err := parseQueryLogFilter(r)
	if err != nil {
		writeQueryLogError(w, http.StatusBadRequest, "invalid time range: "+err.Error())

		return
	}

	total, err := h.source.count(filter)
	if err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	writeQueryLogJSON(w, http.StatusOK, map[string]any{"total": total})
}

// GET /api/querylog/over-time?interval=1h
func (h *queryLogHandler) overTime(w http.ResponseWriter, r *http.Request) {
	filter, err := parseQueryLogFilter(r)
	if err != nil {
		writeQueryLogError(w, http.StatusBadRequest, "invalid time range: "+err.Error())

		return
	}

	interval, err := time.ParseDuration(r.URL.Query().Get("interval"))
	if err != nil || interval <= 0 {
		interval = time.Hour
	}

	items, err := h.source.overTime(filter, interval)
	if err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	writeQueryLogJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/querylog/top?type=domains|clients&filter=all|blocked
func (h *queryLogHandler) top(w http.ResponseWriter, r *http.Request) {
	filter, err := parseQueryLogFilter(r)
	if err != nil {
		writeQueryLogError(w, http.StatusBadRequest, "invalid time range: "+err.Error())

		return
	}

	byClient := r.URL.Query().Get("type") == "clients"
	blockedOnly := r.URL.Query().Get("filter") == "blocked"
	limit := queryInt(r, "limit", 10)
	offset := queryInt(r, "offset", 0)

	items, totalCount, err := h.source.top(filter, byClient, blockedOnly, limit, offset)
	if err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	writeQueryLogJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"totalCount": totalCount,
	})
}

// GET /api/querylog/search?type=domains|clients&q=
func (h *queryLogHandler) search(w http.ResponseWriter, r *http.Request) {
	filter, err := parseQueryLogFilter(r)
	if err != nil {
		writeQueryLogError(w, http.StatusBadRequest, "invalid time range: "+err.Error())

		return
	}

	byClient := r.URL.Query().Get("type") == "clients"
	q := r.URL.Query().Get("q")
	limit := queryInt(r, "limit", 50)

	items, err := h.source.search(filter, byClient, q, limit)
	if err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	writeQueryLogJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/querylog/query-types
func (h *queryLogHandler) queryTypes(w http.ResponseWriter, r *http.Request) {
	filter, err := parseQueryLogFilter(r)
	if err != nil {
		writeQueryLogError(w, http.StatusBadRequest, "invalid time range: "+err.Error())

		return
	}

	items, err := h.source.queryTypes(filter)
	if err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	writeQueryLogJSON(w, http.StatusOK, map[string]any{"items": items})
}
