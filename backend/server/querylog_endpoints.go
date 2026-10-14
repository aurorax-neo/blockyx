package server

// Query log HTTP endpoints for the embedded web UI.
// They expose blocky's own CSV query log files under /api/querylog/*,
// protected by the same API key middleware as the rest of /api.

import (
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

var errQueryLogUnsupported = errors.New("query log API only supports the csv log type")

// queryLogHandler serves the query log endpoints.
type queryLogHandler struct {
	dir string
}

func newQueryLogHandler(cfg *config.Config) (*queryLogHandler, error) {
	if cfg.QueryLog.Type != config.QueryLogTypeCsv {
		return nil, errQueryLogUnsupported
	}

	return &queryLogHandler{dir: cfg.QueryLog.Target.Reveal()}, nil
}

func configureQueryLogHandler(router *chi.Mux, cfg *config.Config) {
	h, err := newQueryLogHandler(cfg)
	if err != nil {
		// Not csv: expose endpoints that explain why they are unavailable.
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

// GET /api/querylog/rows — paginated log rows, newest last.
func (h *queryLogHandler) rows(w http.ResponseWriter, r *http.Request) {
	filter, err := parseQueryLogFilter(r)
	if err != nil {
		writeQueryLogError(w, http.StatusBadRequest, "invalid time range: "+err.Error())

		return
	}

	limit := queryInt(r, "limit", 10)
	offset := queryInt(r, "offset", 0)

	// Keep a sliding window of the newest offset+limit+1 entries;
	// the UI shows newest first.
	want := offset + limit + 1
	window := make([]queryLogEntryJSON, 0, want)
	total := 0

	scanErr := querylog.Scan(h.dir, filter, func(e *querylog.Record) error {
		total++

		if len(window) == want {
			window = window[1:]
		}

		window = append(window, toJSONEntry(e))

		return nil
	})
	if scanErr != nil {
		writeQueryLogError(w, http.StatusInternalServerError, scanErr.Error())

		return
	}

	// Newest first.
	reverseEntries(window)

	hasMore := total > offset+limit
	if len(window) > limit {
		window = window[:limit]
	}

	writeQueryLogJSON(w, http.StatusOK, map[string]any{
		"items":   window,
		"hasMore": hasMore,
	})
}

var errStopScan = errors.New("stop scan")

func reverseEntries(items []queryLogEntryJSON) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

// GET /api/querylog/count
func (h *queryLogHandler) count(w http.ResponseWriter, r *http.Request) {
	filter, err := parseQueryLogFilter(r)
	if err != nil {
		writeQueryLogError(w, http.StatusBadRequest, "invalid time range: "+err.Error())

		return
	}

	total := 0

	if err := querylog.Scan(h.dir, filter, func(*querylog.Record) error {
		total++

		return nil
	}); err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	writeQueryLogJSON(w, http.StatusOK, map[string]any{"total": total})
}

type overTimeBucket struct {
	Time    string `json:"time"`
	Total   int    `json:"total"`
	Blocked int    `json:"blocked"`
	Cached  int    `json:"cached"`
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

	buckets := make(map[string]*overTimeBucket)
	var order []string

	if err := querylog.Scan(h.dir, filter, func(e *querylog.Record) error {
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
	}); err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	sort.Strings(order)

	items := make([]overTimeBucket, 0, len(order))
	for _, k := range order {
		items = append(items, *buckets[k])
	}

	writeQueryLogJSON(w, http.StatusOK, map[string]any{"items": items})
}

type topEntry struct {
	Name    string `json:"name"`
	Total   int    `json:"total"`
	Blocked int    `json:"blocked"`
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

	counts := make(map[string]*topEntry)

	if err := querylog.Scan(h.dir, filter, func(e *querylog.Record) error {
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
	}); err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
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
	q := strings.ToLower(r.URL.Query().Get("q"))
	limit := queryInt(r, "limit", 50)

	counts := make(map[string]int)
	var order []string

	if err := querylog.Scan(h.dir, filter, func(e *querylog.Record) error {
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
	}); err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	type suggestion struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	// Most frequent first.
	sort.Slice(order, func(i, j int) bool {
		if counts[order[i]] != counts[order[j]] {
			return counts[order[i]] > counts[order[j]]
		}

		return order[i] < order[j]
	})

	if len(order) > limit {
		order = order[:limit]
	}

	items := make([]suggestion, 0, len(order))
	for _, name := range order {
		items = append(items, suggestion{Name: name, Count: counts[name]})
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

	counts := make(map[string]int)

	if err := querylog.Scan(h.dir, filter, func(e *querylog.Record) error {
		counts[e.QuestionType]++

		return nil
	}); err != nil {
		writeQueryLogError(w, http.StatusInternalServerError, err.Error())

		return
	}

	type qt struct {
		Type  string `json:"type"`
		Count int    `json:"count"`
	}

	items := make([]qt, 0, len(counts))
	for t, c := range counts {
		items = append(items, qt{Type: t, Count: c})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Count > items[j].Count })

	writeQueryLogJSON(w, http.StatusOK, map[string]any{"items": items})
}
