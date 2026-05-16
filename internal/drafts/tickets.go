package drafts

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// pendingRevision is the work staged by POST /api/drafts/{id}/revisions and
// consumed by GET /api/drafts/{id}/revisions/stream. The split exists because
// the browser's native EventSource client only supports GET requests — so the
// prompt and mode can't ride along on the streaming request itself.
type pendingRevision struct {
	DraftID int
	Prompt  string
	Mode    string
	Expires time.Time
}

// ticketStore holds short-lived pending revisions keyed by an opaque ticket.
//
// The store is in-memory and intentionally non-durable. A pending revision is
// data the client just submitted and hasn't yet started streaming — losing it
// across a server restart is fine; the client retries.
//
// Tickets are one-shot: consuming a ticket removes it. This protects against
// a network-level retry replaying the LLM call (and its bill) twice.
type ticketStore struct {
	mu      sync.Mutex
	items   map[string]pendingRevision
	ttl     time.Duration
	stop    chan struct{}
}

// newTicketStore returns a ticket store and starts a background goroutine
// that purges expired tickets once a minute. Call Close to stop it.
func newTicketStore(ttl time.Duration) *ticketStore {
	s := &ticketStore{
		items: make(map[string]pendingRevision),
		ttl:   ttl,
		stop:  make(chan struct{}),
	}
	go s.gc()
	return s
}

// issue stages a pending revision and returns a fresh ticket id. The id is
// 128 bits of crypto-random hex — enough that a guessed ticket has effectively
// zero chance of matching any live entry.
func (s *ticketStore) issue(p pendingRevision) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])

	p.Expires = time.Now().Add(s.ttl)

	s.mu.Lock()
	s.items[id] = p
	s.mu.Unlock()

	return id, nil
}

// consume looks up a ticket and removes it. The second return is false if
// the ticket is unknown, expired, or already consumed.
func (s *ticketStore) consume(id string) (pendingRevision, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.items[id]
	if !ok {
		return pendingRevision{}, false
	}
	delete(s.items, id)

	if time.Now().After(p.Expires) {
		return pendingRevision{}, false
	}
	return p, true
}

// gc periodically removes expired entries. The interval is coarse — tickets
// are short-lived (minutes), and consume() also re-checks expiry, so the gc
// is just bookkeeping to keep the map small.
func (s *ticketStore) gc() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-t.C:
			s.mu.Lock()
			for id, p := range s.items {
				if now.After(p.Expires) {
					delete(s.items, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

// Close stops the gc goroutine. Safe to call once.
func (s *ticketStore) Close() { close(s.stop) }
