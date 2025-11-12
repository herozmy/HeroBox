package logs

import (
	"sync"
	"time"
)

// Entry 表示一条日志。
type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message"`
	Level     string    `json:"level"`
}

// Buffer 是一个线程安全的环形日志缓冲器。
type Buffer struct {
	mu      sync.RWMutex
	entries []Entry
	limit   int
}

func NewBuffer(limit int) *Buffer {
	if limit <= 0 {
		limit = 200
	}
	return &Buffer{limit: limit}
}

func (b *Buffer) Add(level, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := Entry{Timestamp: time.Now(), Level: level, Message: msg}
	b.entries = append(b.entries, entry)
	if len(b.entries) > b.limit {
		b.entries = b.entries[len(b.entries)-b.limit:]
	}
}

func (b *Buffer) List() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Entry, len(b.entries))
	copy(out, b.entries)
	return out
}
