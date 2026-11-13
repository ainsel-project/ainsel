package signin

import (
	"context"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// MemStore is an in-memory Store for tests and hubs without a database.
type MemStore struct {
	mu       sync.Mutex
	now      func() time.Time
	sessions []*memSession // creation order
}

type memSession struct {
	Session
	redirect string
}

// NewMemStore returns an empty MemStore reading time from now (time.Now if nil).
func NewMemStore(now func() time.Time) *MemStore {
	if now == nil {
		now = time.Now
	}
	return &MemStore{now: now}
}

// view returns the session as callers see it: a live session past its expiry
// reads as expired.
func (m *MemStore) view(s *memSession) Session {
	out := s.Session
	if out.State.Live() && !m.now().Before(out.ExpiresAt) {
		out.State = StateExpired
	}
	return out
}

func (m *MemStore) find(agent, id string) *memSession {
	for _, s := range m.sessions {
		if s.Agent == agent && s.ID == id {
			return s
		}
	}
	return nil
}

// live returns the agent's live, unexpired session.
func (m *MemStore) live(agent string) *memSession {
	for _, s := range m.sessions {
		if s.Agent == agent && m.view(s).State.Live() {
			return s
		}
	}
	return nil
}

// open returns a session that can still advance, or the reason it cannot.
func (m *MemStore) open(agent, id string) (*memSession, error) {
	s := m.find(agent, id)
	if s == nil {
		return nil, ErrNotFound
	}
	if !m.view(s).State.Live() {
		return nil, ErrClosed
	}
	return s, nil
}

func (m *MemStore) Start(_ context.Context, agent string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live(agent) != nil {
		return Session{}, ErrLive
	}
	now := m.now()
	s := &memSession{Session: Session{
		ID: ulid.Make().String(), Agent: agent, State: StateWaitingForPod,
		CreatedAt: now, ExpiresAt: now.Add(TTL),
	}}
	m.sessions = append(m.sessions, s)
	return s.Session, nil
}

func (m *MemStore) Latest(_ context.Context, agent string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sessions) - 1; i >= 0; i-- {
		if m.sessions[i].Agent == agent {
			return m.view(m.sessions[i]), nil
		}
	}
	return Session{}, ErrNotFound
}

func (m *MemStore) Get(_ context.Context, agent, id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.find(agent, id)
	if s == nil {
		return Session{}, ErrNotFound
	}
	return m.view(s), nil
}

func (m *MemStore) SetAuthURL(_ context.Context, agent, id, url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.open(agent, id)
	if err != nil {
		return err
	}
	if s.State != StateWaitingForPod {
		return ErrWrongState
	}
	s.AuthURL = url
	s.State = StateAwaitingRedirect
	return nil
}

func (m *MemStore) SubmitRedirect(_ context.Context, agent, url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.live(agent)
	if s == nil {
		return ErrNotFound
	}
	if s.State != StateAwaitingRedirect {
		return ErrWrongState
	}
	s.redirect = url
	return nil
}

func (m *MemStore) TakeRedirect(_ context.Context, agent, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.open(agent, id)
	if err != nil {
		return "", err
	}
	if s.redirect == "" {
		return "", nil
	}
	url := s.redirect
	s.redirect = ""
	s.State = StateExchanging
	return url, nil
}

func (m *MemStore) Finish(_ context.Context, agent, id string, ok bool, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.open(agent, id)
	if err != nil {
		return err
	}
	s.redirect = ""
	if ok {
		s.State = StateSucceeded
	} else {
		s.State = StateFailed
		s.Error = errMsg
	}
	return nil
}

func (m *MemStore) Cancel(_ context.Context, agent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.live(agent); s != nil {
		s.redirect = ""
		s.State = StateCancelled
	}
	return nil
}

func (m *MemStore) Prune(_ context.Context, cutoff time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.sessions[:0]
	var n int64
	for _, s := range m.sessions {
		if s.CreatedAt.Before(cutoff) {
			n++
			continue
		}
		kept = append(kept, s)
	}
	m.sessions = kept
	return n, nil
}
