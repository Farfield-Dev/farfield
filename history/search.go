package history

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Farfield-Dev/farfield/internal/canonicaljson"
)

const (
	searchCacheSchema      = "farfield.search.cache.v2"
	searchRefreshInterval  = time.Second
	searchFetchConcurrency = 16
	maxPrefixExpansions    = 512
)

// SearchQuery combines BM25-ranked full-text search with exact record filters.
// Bare terms are ANDed, quoted text is a phrase, and a trailing * is a prefix.
type SearchQuery struct {
	Text           string
	ConversationID string
	TraceID        string
	Kind           string
	Agent          string
	Tool           string
	Status         string
	Tags           map[string]string
	Since          *time.Time
	Until          *time.Time
	Limit          int
}

type SearchHit struct {
	Record  Record  `json:"record"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

type SearchResult struct {
	Hits           []SearchHit `json:"hits"`
	Total          int         `json:"total"`
	TookMS         float64     `json:"took_ms"`
	IndexedRecords int         `json:"indexed_records"`
	IndexUpdatedAt time.Time   `json:"index_updated_at"`
}

type SearchIndexStatus struct {
	IndexedRecords int       `json:"indexed_records"`
	SourceSegments int       `json:"source_segments"`
	IndexUpdatedAt time.Time `json:"index_updated_at"`
}

type searchDocument struct {
	SourceKey string `json:"source_key"`
	Record    Record `json:"record"`
	Text      string `json:"text"`
}

type searchCache struct {
	SchemaVersion string            `json:"schema_version"`
	Store         string            `json:"store"`
	Sources       map[string]string `json:"sources"`
	Documents     []searchDocument  `json:"documents"`
	CacheSHA256   string            `json:"cache_sha256,omitempty"`
}

type searchToken struct {
	term     string
	position int
}

type searchClause struct {
	terms  []string
	phrase bool
	prefix bool
}

type searchProjection struct {
	service   *Service
	cachePath string
	now       func() time.Time

	refreshMu       sync.Mutex
	mu              sync.RWMutex
	loaded          bool
	refreshing      bool
	persisting      bool
	documents       []searchDocument
	sources         map[string]string
	postings        map[string]map[int][]int
	filters         map[string]map[string]map[int]struct{}
	vocabulary      []string
	documentLengths []int
	totalTerms      int
	lastRefresh     time.Time
	updatedAt       time.Time
}

func newSearchProjection(service *Service, cachePath string, now func() time.Time) *searchProjection {
	return &searchProjection{service: service, cachePath: cachePath, now: now}
}

// Search executes entirely against the local disposable index after the first
// synchronization. Refreshes for external writers run asynchronously so a
// warm query never waits on an object-store LIST.
func (service *Service) Search(ctx context.Context, query SearchQuery) (SearchResult, error) {
	started := time.Now()
	if err := validateSearchQuery(query); err != nil {
		return SearchResult{}, err
	}
	if err := service.search.ensureLoaded(ctx); err != nil {
		return SearchResult{}, failure("FH_SEARCH_INDEX_FAILED", "search index could not be synchronized", err)
	}
	service.search.refreshAsync()
	result, err := service.search.execute(query)
	if err != nil {
		return SearchResult{}, err
	}
	result.TookMS = float64(time.Since(started).Microseconds()) / 1000
	return result, nil
}

func (projection *searchProjection) query(ctx context.Context, query Query) ([]Record, error) {
	if query.Limit < 0 || query.Limit > 1000 {
		return nil, failure("FH_INVALID_SEARCH", "query limit must be between 1 and 1000", nil)
	}
	if err := projection.ensureLoaded(ctx); err != nil {
		return nil, failure("FH_SEARCH_INDEX_FAILED", "query index could not be synchronized", err)
	}
	projection.refreshAsync()
	projection.mu.RLock()
	defer projection.mu.RUnlock()
	candidates := make(map[int]float64, len(projection.documents))
	for index := range projection.documents {
		candidates[index] = 0
	}
	for field, value := range map[string]string{
		"conversation_id": query.ConversationID, "trace_id": query.TraceID,
		"kind": query.Kind, "agent": query.Agent, "tool": query.Tool, "status": query.Status,
	} {
		if value != "" {
			candidates = filterScores(candidates, projection.filters[field][value])
		}
	}
	for key, value := range query.Tags {
		candidates = filterScores(candidates, projection.filters["tag"][key+"\x00"+value])
	}
	records := make([]Record, 0, len(candidates))
	for docID := range candidates {
		record := projection.documents[docID].Record
		if query.Since != nil && record.OccurredAt.Before(*query.Since) || query.Until != nil && record.OccurredAt.After(*query.Until) {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].OccurredAt.Equal(records[right].OccurredAt) {
			if records[left].Sequence != nil && records[right].Sequence != nil && *records[left].Sequence != *records[right].Sequence {
				return *records[left].Sequence < *records[right].Sequence
			}
			return records[left].ID < records[right].ID
		}
		return records[left].OccurredAt.Before(records[right].OccurredAt)
	})
	limit := query.Limit
	if limit == 0 {
		limit = 100
	}
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

// RebuildSearchIndex discards only the disposable in-memory view, re-reads all
// authoritative segments, and atomically replaces the optional local cache.
func (service *Service) RebuildSearchIndex(ctx context.Context) (SearchIndexStatus, error) {
	projection := service.search
	projection.refreshMu.Lock()
	defer projection.refreshMu.Unlock()
	projection.mu.Lock()
	projection.loaded = true
	projection.documents = nil
	projection.sources = map[string]string{}
	projection.rebuildLocked()
	projection.lastRefresh = time.Time{}
	projection.updatedAt = projection.now().UTC()
	projection.mu.Unlock()
	if err := projection.refresh(ctx); err != nil {
		return SearchIndexStatus{}, failure("FH_SEARCH_INDEX_FAILED", "search index could not be rebuilt", err)
	}
	projection.mu.RLock()
	defer projection.mu.RUnlock()
	return SearchIndexStatus{IndexedRecords: len(projection.documents), SourceSegments: len(projection.sources), IndexUpdatedAt: projection.updatedAt}, nil
}

func validateSearchQuery(query SearchQuery) error {
	if query.Limit == 0 {
		query.Limit = 100
	}
	if query.Limit < 0 || query.Limit > 1000 {
		return failure("FH_INVALID_SEARCH", "search limit must be between 1 and 1000", nil)
	}
	if query.Since != nil && query.Until != nil && query.Since.After(*query.Until) {
		return failure("FH_INVALID_SEARCH", "search since cannot be after until", nil)
	}
	_, err := parseSearchQuery(query.Text)
	return err
}

func (projection *searchProjection) ensureLoaded(ctx context.Context) error {
	projection.mu.RLock()
	loaded := projection.loaded
	projection.mu.RUnlock()
	if loaded {
		return nil
	}
	projection.refreshMu.Lock()
	defer projection.refreshMu.Unlock()
	projection.mu.RLock()
	loaded = projection.loaded
	projection.mu.RUnlock()
	if loaded {
		return nil
	}
	if projection.cachePath != "" {
		if cache, err := projection.readCache(); err == nil {
			projection.mu.Lock()
			projection.installCacheLocked(cache)
			projection.mu.Unlock()
			// A verified cache can serve immediately. Search starts the remote
			// freshness check asynchronously after returning the local result.
			return nil
		}
	}
	projection.mu.Lock()
	if !projection.loaded {
		projection.loaded = true
		projection.sources = map[string]string{}
		projection.documents = nil
		projection.rebuildLocked()
	}
	projection.mu.Unlock()
	return projection.refresh(ctx)
}

func (projection *searchProjection) refreshAsync() {
	projection.mu.Lock()
	if projection.refreshing || projection.now().Sub(projection.lastRefresh) < searchRefreshInterval {
		projection.mu.Unlock()
		return
	}
	projection.refreshing = true
	projection.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		projection.refreshMu.Lock()
		_ = projection.refresh(ctx)
		projection.refreshMu.Unlock()
		projection.mu.Lock()
		projection.refreshing = false
		projection.mu.Unlock()
	}()
}

func (projection *searchProjection) refresh(ctx context.Context) error {
	keys, err := projection.service.store.List(ctx, historySegmentsPrefix)
	if err != nil {
		return err
	}
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}
	projection.mu.RLock()
	missing := make([]string, 0)
	removed := false
	for source := range projection.sources {
		if _, ok := keySet[source]; !ok {
			removed = true
			break
		}
	}
	if !removed {
		for _, key := range keys {
			if _, ok := projection.sources[key]; !ok {
				missing = append(missing, key)
			}
		}
	}
	projection.mu.RUnlock()
	if removed {
		projection.mu.Lock()
		projection.documents = nil
		projection.sources = map[string]string{}
		projection.rebuildLocked()
		projection.mu.Unlock()
		missing = append(missing[:0], keys...)
	}

	type loadedSource struct {
		key       string
		digest    string
		documents []searchDocument
	}
	loaded := make([]loadedSource, len(missing))
	jobs := make(chan int)
	workers := min(searchFetchConcurrency, len(missing))
	var wait sync.WaitGroup
	var firstError error
	var errorOnce sync.Once
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				segment, readErr := projection.service.readSegmentAt(ctx, missing[index])
				if readErr != nil {
					errorOnce.Do(func() { firstError = readErr })
					continue
				}
				documents, buildErr := projection.documentsForSegment(ctx, missing[index], segment)
				if buildErr != nil {
					errorOnce.Do(func() { firstError = buildErr })
					continue
				}
				loaded[index] = loadedSource{key: missing[index], digest: segment.SegmentSHA256, documents: documents}
			}
		}()
	}
	for index := range missing {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	if firstError != nil {
		return firstError
	}
	projection.mu.Lock()
	for _, source := range loaded {
		projection.sources[source.key] = source.digest
		projection.documents = append(projection.documents, source.documents...)
	}
	if len(loaded) > 0 || removed {
		projection.rebuildLocked()
		projection.updatedAt = projection.now().UTC()
	}
	projection.lastRefresh = projection.now()
	cache := projection.cacheLocked()
	projection.mu.Unlock()
	if projection.cachePath != "" && (len(loaded) > 0 || removed) {
		// The cache is an accelerator. A full disk or read-only cache path
		// cannot turn a valid authoritative query into a failure.
		_ = projection.writeCache(cache)
	}
	return nil
}

func (projection *searchProjection) documentsForSegment(ctx context.Context, sourceKey string, segment Segment) ([]searchDocument, error) {
	segments := map[string]Segment{sourceKey: segment}
	documents := make([]searchDocument, len(segment.Entries))
	for index, entry := range segment.Entries {
		content, err := projection.service.readContent(ctx, entry.Record, segments)
		if err != nil {
			return nil, err
		}
		text, err := searchableJSON(content)
		if err != nil {
			return nil, err
		}
		documents[index] = searchDocument{SourceKey: sourceKey, Record: entry.Record, Text: text}
	}
	return documents, nil
}

// observeSegment makes writes through this process immediately searchable once
// the index has been opened. Failures are intentionally non-authoritative: the
// next synchronization repairs the disposable view from the committed segment.
func (projection *searchProjection) observeSegment(ctx context.Context, sourceKey string, segment Segment) {
	projection.mu.RLock()
	loaded := projection.loaded
	_, applied := projection.sources[sourceKey]
	projection.mu.RUnlock()
	if !loaded || applied {
		return
	}
	documents, err := projection.documentsForSegment(ctx, sourceKey, segment)
	if err != nil {
		return
	}
	projection.mu.Lock()
	if _, applied := projection.sources[sourceKey]; !applied {
		projection.sources[sourceKey] = segment.SegmentSHA256
		projection.addDocumentsLocked(documents)
		projection.updatedAt = projection.now().UTC()
		projection.lastRefresh = projection.now()
	}
	projection.mu.Unlock()
	projection.persistAsync()
}

func searchableJSON(content []byte) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	parts := make([]string, 0)
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if !searchableJSONField(key) {
					continue
				}
				parts = append(parts, key)
				visit(typed[key])
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case string:
			parts = append(parts, typed)
		case json.Number:
			parts = append(parts, typed.String())
		case bool:
			parts = append(parts, fmt.Sprint(typed))
		}
	}
	visit(value)
	return strings.Join(parts, " \n"), nil
}

// Provider payloads often contain large opaque cryptographic material. It is
// evidence in authoritative History but has no lexical search value and would
// otherwise dominate index size and snippets.
func searchableJSONField(key string) bool {
	normalized := strings.ToLower(key)
	return !strings.Contains(normalized, "encrypted") && !strings.Contains(normalized, "signature")
}

func tokenize(text string) []searchToken {
	tokens := make([]searchToken, 0)
	var word []rune
	position := 0
	flush := func() {
		if len(word) == 0 {
			return
		}
		tokens = append(tokens, searchToken{term: strings.ToLower(string(word)), position: position})
		position++
		word = word[:0]
	}
	for _, value := range text {
		if unicode.IsLetter(value) || unicode.IsNumber(value) {
			word = append(word, unicode.ToLower(value))
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func (projection *searchProjection) rebuildLocked() {
	projection.postings = make(map[string]map[int][]int)
	projection.filters = make(map[string]map[string]map[int]struct{})
	projection.totalTerms = 0
	projection.documentLengths = make([]int, len(projection.documents))
	for docID, document := range projection.documents {
		tokens := tokenize(document.Text)
		projection.documentLengths[docID] = len(tokens)
		projection.totalTerms += len(tokens)
		for _, token := range tokens {
			byDocument := projection.postings[token.term]
			if byDocument == nil {
				byDocument = make(map[int][]int)
				projection.postings[token.term] = byDocument
			}
			byDocument[docID] = append(byDocument[docID], token.position)
		}
		record := document.Record
		projection.addFilterLocked("conversation_id", record.ConversationID, docID)
		projection.addFilterLocked("trace_id", pointerValue(record.TraceID), docID)
		projection.addFilterLocked("kind", record.Kind, docID)
		projection.addFilterLocked("agent", pointerValue(record.Agent), docID)
		projection.addFilterLocked("tool", pointerValue(record.Tool), docID)
		projection.addFilterLocked("status", pointerValue(record.Status), docID)
		for key, value := range record.Tags {
			projection.addFilterLocked("tag", key+"\x00"+value, docID)
		}
	}
	projection.vocabulary = make([]string, 0, len(projection.postings))
	for term := range projection.postings {
		projection.vocabulary = append(projection.vocabulary, term)
	}
	sort.Strings(projection.vocabulary)
}

func (projection *searchProjection) addDocumentsLocked(documents []searchDocument) {
	start := len(projection.documents)
	projection.documents = append(projection.documents, documents...)
	projection.documentLengths = append(projection.documentLengths, make([]int, len(documents))...)
	for offset, document := range documents {
		docID := start + offset
		tokens := tokenize(document.Text)
		projection.documentLengths[docID] = len(tokens)
		projection.totalTerms += len(tokens)
		for _, token := range tokens {
			byDocument := projection.postings[token.term]
			if byDocument == nil {
				byDocument = make(map[int][]int)
				projection.postings[token.term] = byDocument
				projection.vocabulary = append(projection.vocabulary, token.term)
			}
			byDocument[docID] = append(byDocument[docID], token.position)
		}
		record := document.Record
		projection.addFilterLocked("conversation_id", record.ConversationID, docID)
		projection.addFilterLocked("trace_id", pointerValue(record.TraceID), docID)
		projection.addFilterLocked("kind", record.Kind, docID)
		projection.addFilterLocked("agent", pointerValue(record.Agent), docID)
		projection.addFilterLocked("tool", pointerValue(record.Tool), docID)
		projection.addFilterLocked("status", pointerValue(record.Status), docID)
		for key, value := range record.Tags {
			projection.addFilterLocked("tag", key+"\x00"+value, docID)
		}
	}
	sort.Strings(projection.vocabulary)
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (projection *searchProjection) addFilterLocked(field, value string, docID int) {
	if value == "" {
		return
	}
	byValue := projection.filters[field]
	if byValue == nil {
		byValue = make(map[string]map[int]struct{})
		projection.filters[field] = byValue
	}
	documents := byValue[value]
	if documents == nil {
		documents = make(map[int]struct{})
		byValue[value] = documents
	}
	documents[docID] = struct{}{}
}

func parseSearchQuery(text string) ([]searchClause, error) {
	text = strings.TrimSpace(text)
	clauses := make([]searchClause, 0)
	for len(text) > 0 {
		text = strings.TrimLeftFunc(text, unicode.IsSpace)
		if text == "" {
			break
		}
		if text[0] == '"' {
			end := strings.IndexByte(text[1:], '"')
			if end < 0 {
				return nil, failure("FH_INVALID_SEARCH", "search phrase is missing its closing quote", nil)
			}
			phrase := text[1 : end+1]
			values := tokenize(phrase)
			if len(values) == 0 {
				return nil, failure("FH_INVALID_SEARCH", "search phrase contains no searchable text", nil)
			}
			terms := make([]string, len(values))
			for index, value := range values {
				terms[index] = value.term
			}
			clauses = append(clauses, searchClause{terms: terms, phrase: true})
			text = text[end+2:]
			continue
		}
		end := strings.IndexFunc(text, unicode.IsSpace)
		word := text
		if end >= 0 {
			word = text[:end]
			text = text[end:]
		} else {
			text = ""
		}
		prefix := strings.HasSuffix(word, "*")
		if prefix {
			word = strings.TrimSuffix(word, "*")
		}
		values := tokenize(word)
		if len(values) != 1 {
			return nil, failure("FH_INVALID_SEARCH", fmt.Sprintf("search term %q is invalid", word), nil)
		}
		if prefix && len([]rune(values[0].term)) < 2 {
			return nil, failure("FH_INVALID_SEARCH", "search prefixes must contain at least two characters", nil)
		}
		clauses = append(clauses, searchClause{terms: []string{values[0].term}, prefix: prefix})
	}
	return clauses, nil
}

func (projection *searchProjection) execute(query SearchQuery) (SearchResult, error) {
	clauses, err := parseSearchQuery(query.Text)
	if err != nil {
		return SearchResult{}, err
	}
	limit := query.Limit
	if limit == 0 {
		limit = 100
	}
	projection.mu.RLock()
	defer projection.mu.RUnlock()

	var candidates map[int]float64
	for _, clause := range clauses {
		matches, clauseErr := projection.matchClauseLocked(clause)
		if clauseErr != nil {
			return SearchResult{}, clauseErr
		}
		candidates = intersectScores(candidates, matches)
	}
	if candidates == nil {
		candidates = make(map[int]float64, len(projection.documents))
		for index := range projection.documents {
			candidates[index] = 0
		}
	}
	for field, value := range map[string]string{
		"conversation_id": query.ConversationID, "trace_id": query.TraceID,
		"kind": query.Kind, "agent": query.Agent, "tool": query.Tool, "status": query.Status,
	} {
		if value != "" {
			candidates = filterScores(candidates, projection.filters[field][value])
		}
	}
	for key, value := range query.Tags {
		candidates = filterScores(candidates, projection.filters["tag"][key+"\x00"+value])
	}
	for docID := range candidates {
		occurredAt := projection.documents[docID].Record.OccurredAt
		if query.Since != nil && occurredAt.Before(*query.Since) || query.Until != nil && occurredAt.After(*query.Until) {
			delete(candidates, docID)
		}
	}
	type scored struct {
		docID int
		score float64
	}
	values := make([]scored, 0, len(candidates))
	for docID, score := range candidates {
		values = append(values, scored{docID: docID, score: score})
	}
	sort.Slice(values, func(left, right int) bool {
		if values[left].score != values[right].score {
			return values[left].score > values[right].score
		}
		leftRecord := projection.documents[values[left].docID].Record
		rightRecord := projection.documents[values[right].docID].Record
		if !leftRecord.OccurredAt.Equal(rightRecord.OccurredAt) {
			return leftRecord.OccurredAt.After(rightRecord.OccurredAt)
		}
		return leftRecord.ID < rightRecord.ID
	})
	total := len(values)
	if len(values) > limit {
		values = values[:limit]
	}
	hits := make([]SearchHit, len(values))
	for index, value := range values {
		document := projection.documents[value.docID]
		hits[index] = SearchHit{Record: document.Record, Score: value.score, Snippet: searchSnippet(document.Text, clauses)}
	}
	return SearchResult{
		Hits: hits, Total: total, IndexedRecords: len(projection.documents),
		IndexUpdatedAt: projection.updatedAt,
	}, nil
}

func (projection *searchProjection) matchClauseLocked(clause searchClause) (map[int]float64, error) {
	terms := clause.terms
	if clause.prefix {
		prefix := terms[0]
		start := sort.SearchStrings(projection.vocabulary, prefix)
		terms = terms[:0]
		for index := start; index < len(projection.vocabulary) && strings.HasPrefix(projection.vocabulary[index], prefix); index++ {
			terms = append(terms, projection.vocabulary[index])
			if len(terms) > maxPrefixExpansions {
				return nil, failure("FH_SEARCH_PREFIX_TOO_BROAD", fmt.Sprintf("prefix %q matches more than %d terms", prefix, maxPrefixExpansions), nil)
			}
		}
		matches := make(map[int]float64)
		for _, term := range terms {
			for docID, score := range projection.termScoresLocked(term) {
				matches[docID] += score
			}
		}
		return matches, nil
	}
	var matches map[int]float64
	for _, term := range terms {
		matches = intersectScores(matches, projection.termScoresLocked(term))
	}
	if clause.phrase {
		for docID := range matches {
			if !projection.hasPhraseLocked(docID, terms) {
				delete(matches, docID)
			} else {
				matches[docID] *= 2
			}
		}
	}
	if matches == nil {
		matches = map[int]float64{}
	}
	return matches, nil
}

func (projection *searchProjection) termScoresLocked(term string) map[int]float64 {
	posting := projection.postings[term]
	result := make(map[int]float64, len(posting))
	if len(posting) == 0 || len(projection.documents) == 0 {
		return result
	}
	n := float64(len(projection.documents))
	df := float64(len(posting))
	idf := math.Log(1 + (n-df+0.5)/(df+0.5))
	averageLength := float64(projection.totalTerms) / n
	for docID, positions := range posting {
		tf := float64(len(positions))
		documentLength := float64(projection.documentLengths[docID])
		denominator := tf + 1.2*(1-0.75+0.75*documentLength/averageLength)
		result[docID] = idf * (tf * 2.2 / denominator)
	}
	return result
}

func (projection *searchProjection) hasPhraseLocked(docID int, terms []string) bool {
	if len(terms) < 2 {
		return true
	}
	positions := projection.postings[terms[0]][docID]
	for _, start := range positions {
		matched := true
		for offset := 1; offset < len(terms); offset++ {
			if !containsPosition(projection.postings[terms[offset]][docID], start+offset) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func containsPosition(values []int, expected int) bool {
	index := sort.SearchInts(values, expected)
	return index < len(values) && values[index] == expected
}

func intersectScores(left, right map[int]float64) map[int]float64 {
	if left == nil {
		result := make(map[int]float64, len(right))
		for key, value := range right {
			result[key] = value
		}
		return result
	}
	if len(left) > len(right) {
		left, right = right, left
	}
	result := make(map[int]float64)
	for key, value := range left {
		if other, ok := right[key]; ok {
			result[key] = value + other
		}
	}
	return result
}

func filterScores(scores map[int]float64, allowed map[int]struct{}) map[int]float64 {
	result := make(map[int]float64)
	for docID, score := range scores {
		if _, ok := allowed[docID]; ok {
			result[docID] = score
		}
	}
	return result
}

func searchSnippet(text string, clauses []searchClause) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return ""
	}
	needle := ""
	if len(clauses) > 0 && len(clauses[0].terms) > 0 {
		needle = clauses[0].terms[0]
	}
	lower := []rune(strings.ToLower(string(runes)))
	needleRunes := []rune(needle)
	match := 0
	if len(needleRunes) > 0 {
		for index := 0; index+len(needleRunes) <= len(lower); index++ {
			if string(lower[index:index+len(needleRunes)]) == string(needleRunes) {
				match = index
				break
			}
		}
	}
	start := max(0, match-80)
	end := min(len(runes), start+240)
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(runes) {
		suffix = "…"
	}
	return prefix + strings.TrimSpace(string(runes[start:end])) + suffix
}

func (projection *searchProjection) cacheLocked() searchCache {
	documents := append([]searchDocument(nil), projection.documents...)
	sort.Slice(documents, func(left, right int) bool {
		if documents[left].SourceKey == documents[right].SourceKey {
			return documents[left].Record.ID < documents[right].Record.ID
		}
		return documents[left].SourceKey < documents[right].SourceKey
	})
	sources := make(map[string]string, len(projection.sources))
	for key, value := range projection.sources {
		sources[key] = value
	}
	cache := searchCache{SchemaVersion: searchCacheSchema, Store: projection.service.store.Description(), Sources: sources, Documents: documents}
	cache.CacheSHA256, _ = hashSearchCache(cache)
	return cache
}

func hashSearchCache(cache searchCache) (string, error) {
	cache.CacheSHA256 = ""
	encoded, err := canonicaljson.Marshal(cache)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (projection *searchProjection) installCacheLocked(cache searchCache) {
	projection.loaded = true
	projection.documents = cache.Documents
	projection.sources = cache.Sources
	projection.rebuildLocked()
	projection.updatedAt = projection.now().UTC()
}

func (projection *searchProjection) readCache() (searchCache, error) {
	file, err := os.Open(projection.cachePath)
	if err != nil {
		return searchCache{}, err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(io.LimitReader(file, 1<<30))
	if err != nil {
		return searchCache{}, err
	}
	defer compressed.Close()
	decoder := json.NewDecoder(compressed)
	decoder.DisallowUnknownFields()
	var cache searchCache
	if err := decoder.Decode(&cache); err != nil {
		return searchCache{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return searchCache{}, fmt.Errorf("search cache contains trailing data")
	}
	if cache.SchemaVersion != searchCacheSchema || cache.Store != projection.service.store.Description() || cache.Sources == nil {
		return searchCache{}, fmt.Errorf("search cache identity is invalid")
	}
	digest, err := hashSearchCache(cache)
	if err != nil || digest != cache.CacheSHA256 {
		return searchCache{}, fmt.Errorf("search cache checksum is invalid")
	}
	return cache, nil
}

func (projection *searchProjection) writeCache(cache searchCache) error {
	encoded, err := canonicaljson.Marshal(cache)
	if err != nil {
		return err
	}
	directory := filepath.Dir(projection.cachePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".farfield-search-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	compressed := gzip.NewWriter(temporary)
	if _, err := compressed.Write(encoded); err != nil {
		compressed.Close()
		temporary.Close()
		return err
	}
	if err := compressed.Close(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, projection.cachePath); err != nil {
		return err
	}
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}

func (projection *searchProjection) persistAsync() {
	if projection.cachePath == "" {
		return
	}
	projection.mu.Lock()
	if projection.persisting {
		projection.mu.Unlock()
		return
	}
	projection.persisting = true
	projection.mu.Unlock()
	go func() {
		projection.refreshMu.Lock()
		projection.mu.RLock()
		cache := projection.cacheLocked()
		projection.mu.RUnlock()
		_ = projection.writeCache(cache)
		projection.refreshMu.Unlock()
		projection.mu.Lock()
		projection.persisting = false
		projection.mu.Unlock()
	}()
}
