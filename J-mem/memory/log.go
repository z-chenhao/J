// Package memory provides inspectable JSONL-backed long-term memory and four
// ordinary J-agent tools.
package memory

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z-chenhao/J/J-agent/agent"
)

const (
	formatVersion   = 1
	maxContentBytes = 32 * 1024
	maxQueryBytes   = 4 * 1024
	maxLineBytes    = 1 << 20
	defaultLimit    = 10
	maxLimit        = 100
)

var (
	// ErrNotFound reports that a memory ID has no active record.
	ErrNotFound = errors.New("memory not found")
)

// Record is the current materialized state of one long-term memory.
type Record struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Log owns one append-only JSONL memory file.
//
// One Log should be shared by all callers writing a given path. Cross-process
// write coordination is deliberately not provided in version 0.1.
type Log struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

// Open creates or opens one local JSONL memory log.
func Open(path string) (*Log, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("memory log path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve memory log path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create memory log directory: %w", err)
	}
	handle, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create memory log: %w", err)
	}
	if err := handle.Close(); err != nil {
		return nil, fmt.Errorf("close memory log: %w", err)
	}
	log := &Log{
		path: absolute,
		now:  func() time.Time { return time.Now().UTC() },
	}
	if _, err := log.readState(context.Background()); err != nil {
		return nil, err
	}
	return log, nil
}

// Retrieve returns bounded candidate memories for model-side relevance
// selection. Case-insensitive phrase and term matches rank first; the most
// recently updated active memories fill any remaining capacity. An empty query
// lists recent memories.
func (log *Log) Retrieve(ctx context.Context, query string, limit int) ([]Record, error) {
	if err := validateLog(log, ctx); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if len(query) > maxQueryBytes {
		return nil, fmt.Errorf("memory query exceeds %d bytes", maxQueryBytes)
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maxLimit {
		return nil, fmt.Errorf("memory limit must be between 1 and %d", maxLimit)
	}

	log.mu.Lock()
	defer log.mu.Unlock()
	state, err := log.readState(ctx)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(query)
	terms := queryTerms(needle)
	candidates := make([]retrievalCandidate, 0, len(state))
	for _, current := range state {
		if !current.active {
			continue
		}
		content := strings.ToLower(current.record.Content)
		candidates = append(candidates, retrievalCandidate{
			materializedRecord: current,
			score:              lexicalScore(content, needle, terms),
		})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].score != candidates[right].score {
			return candidates[left].score > candidates[right].score
		}
		leftTime := candidates[left].record.UpdatedAt
		rightTime := candidates[right].record.UpdatedAt
		if leftTime.Equal(rightTime) {
			return candidates[left].sequence > candidates[right].sequence
		}
		return leftTime.After(rightTime)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	records := make([]Record, len(candidates))
	for index, current := range candidates {
		records[index] = current.record
	}
	return records, nil
}

type retrievalCandidate struct {
	materializedRecord
	score int
}

func queryTerms(query string) []string {
	if query == "" {
		return nil
	}
	seen := make(map[string]struct{})
	terms := make([]string, 0)
	for _, term := range strings.Fields(query) {
		if term == query || len(term) < 2 {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func lexicalScore(content, query string, terms []string) int {
	if query == "" {
		return 0
	}
	if strings.Contains(content, query) {
		return 1 + len(terms)
	}
	score := 0
	for _, term := range terms {
		if strings.Contains(content, term) {
			score++
		}
	}
	return score
}

// Store appends one new long-term memory.
func (log *Log) Store(ctx context.Context, content string) (Record, error) {
	if err := validateLog(log, ctx); err != nil {
		return Record{}, err
	}
	content, err := validateContent(content)
	if err != nil {
		return Record{}, err
	}

	log.mu.Lock()
	defer log.mu.Unlock()
	state, err := log.readState(ctx)
	if err != nil {
		return Record{}, err
	}
	id, err := newID()
	if err != nil {
		return Record{}, fmt.Errorf("create memory ID: %w", err)
	}
	for {
		if _, exists := state[id]; !exists {
			break
		}
		id, err = newID()
		if err != nil {
			return Record{}, fmt.Errorf("create memory ID: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	at := log.now().UTC()
	event := logEvent{
		Version: formatVersion,
		Op:      operationStore,
		ID:      id,
		Content: content,
		At:      at,
	}
	if err := log.append(event); err != nil {
		return Record{}, err
	}
	return Record{ID: id, Content: content, CreatedAt: at, UpdatedAt: at}, nil
}

// Modify appends a replacement value for one active memory.
func (log *Log) Modify(ctx context.Context, id, content string) (Record, error) {
	if err := validateLog(log, ctx); err != nil {
		return Record{}, err
	}
	id, err := validateID(id)
	if err != nil {
		return Record{}, err
	}
	content, err = validateContent(content)
	if err != nil {
		return Record{}, err
	}

	log.mu.Lock()
	defer log.mu.Unlock()
	state, err := log.readState(ctx)
	if err != nil {
		return Record{}, err
	}
	current, ok := state[id]
	if !ok || !current.active {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	at := log.now().UTC()
	if err := log.append(logEvent{
		Version: formatVersion,
		Op:      operationModify,
		ID:      id,
		Content: content,
		At:      at,
	}); err != nil {
		return Record{}, err
	}
	current.record.Content = content
	current.record.UpdatedAt = at
	return current.record, nil
}

// Forget appends a tombstone for one active memory.
func (log *Log) Forget(ctx context.Context, id string) error {
	if err := validateLog(log, ctx); err != nil {
		return err
	}
	id, err := validateID(id)
	if err != nil {
		return err
	}

	log.mu.Lock()
	defer log.mu.Unlock()
	state, err := log.readState(ctx)
	if err != nil {
		return err
	}
	current, ok := state[id]
	if !ok || !current.active {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return log.append(logEvent{
		Version: formatVersion,
		Op:      operationForget,
		ID:      id,
		At:      log.now().UTC(),
	})
}

// Tools returns retrieve, store, modify, and forget as ordinary J-agent Tools.
func (log *Log) Tools() []agent.Tool {
	return []agent.Tool{
		&memoryTool{log: log, kind: toolRetrieve},
		&memoryTool{log: log, kind: toolStore},
		&memoryTool{log: log, kind: toolModify},
		&memoryTool{log: log, kind: toolForget},
	}
}

type operation string

const (
	operationStore  operation = "store"
	operationModify operation = "modify"
	operationForget operation = "forget"
)

type logEvent struct {
	Version int       `json:"version"`
	Op      operation `json:"op"`
	ID      string    `json:"id"`
	Content string    `json:"content,omitempty"`
	At      time.Time `json:"at"`
}

type materializedRecord struct {
	record   Record
	sequence int
	active   bool
}

func (log *Log) readState(ctx context.Context) (map[string]materializedRecord, error) {
	handle, err := os.Open(log.path)
	if err != nil {
		return nil, fmt.Errorf("open memory log: %w", err)
	}
	defer handle.Close()

	state := make(map[string]materializedRecord)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		event, err := decodeEvent(scanner.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode memory log line %d: %w", line, err)
		}
		switch event.Op {
		case operationStore:
			if _, exists := seen[event.ID]; exists {
				return nil, fmt.Errorf("memory log line %d stores duplicate ID %q", line, event.ID)
			}
			seen[event.ID] = struct{}{}
			state[event.ID] = materializedRecord{record: Record{
				ID:        event.ID,
				Content:   event.Content,
				CreatedAt: event.At,
				UpdatedAt: event.At,
			}, sequence: line, active: true}
		case operationModify:
			current, exists := state[event.ID]
			if !exists || !current.active {
				return nil, fmt.Errorf("memory log line %d modifies unknown ID %q", line, event.ID)
			}
			current.record.Content = event.Content
			current.record.UpdatedAt = event.At
			current.sequence = line
			state[event.ID] = current
		case operationForget:
			current, exists := state[event.ID]
			if !exists || !current.active {
				return nil, fmt.Errorf("memory log line %d forgets unknown ID %q", line, event.ID)
			}
			current.active = false
			current.sequence = line
			state[event.ID] = current
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read memory log: %w", err)
	}
	return state, nil
}

func decodeEvent(data []byte) (logEvent, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var event logEvent
	if err := decoder.Decode(&event); err != nil {
		return logEvent{}, err
	}
	if err := requireJSONEnd(decoder); err != nil {
		return logEvent{}, err
	}
	if event.Version != formatVersion {
		return logEvent{}, fmt.Errorf("unsupported format version %d", event.Version)
	}
	id, err := validateID(event.ID)
	if err != nil {
		return logEvent{}, err
	}
	event.ID = id
	if event.At.IsZero() {
		return logEvent{}, errors.New("memory event timestamp is required")
	}
	event.At = event.At.UTC()
	switch event.Op {
	case operationStore, operationModify:
		content, err := validateContent(event.Content)
		if err != nil {
			return logEvent{}, err
		}
		event.Content = content
	case operationForget:
		if event.Content != "" {
			return logEvent{}, errors.New("forget event cannot contain content")
		}
	default:
		return logEvent{}, fmt.Errorf("unsupported memory operation %q", event.Op)
	}
	return event, nil
}

func (log *Log) append(event logEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode memory event: %w", err)
	}
	data = append(data, '\n')
	handle, err := os.OpenFile(log.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open memory log for append: %w", err)
	}
	written, err := handle.Write(data)
	if err != nil {
		_ = handle.Close()
		return fmt.Errorf("append memory event: %w", err)
	}
	if written != len(data) {
		_ = handle.Close()
		return fmt.Errorf("append memory event: %w", io.ErrShortWrite)
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return fmt.Errorf("sync memory event: %w", err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("close memory log: %w", err)
	}
	return nil
}

func validateLog(log *Log, ctx context.Context) error {
	if log == nil {
		return errors.New("memory log is required")
	}
	if ctx == nil {
		return errors.New("memory context is required")
	}
	return ctx.Err()
}

func validateID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("memory ID is required")
	}
	if len(id) > 128 {
		return "", errors.New("memory ID exceeds 128 bytes")
	}
	return id, nil
}

func validateContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("memory content is required")
	}
	if len(content) > maxContentBytes {
		return "", fmt.Errorf("memory content exceeds %d bytes", maxContentBytes)
	}
	return content, nil
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "mem_" + hex.EncodeToString(value[:]), nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("unexpected data after the JSON object")
}
