package session

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrSessionNotFound = errors.New("session not found")

type CreateParams struct {
	CallID    string
	Caller    string
	Providers ProviderBindings
	Stream    StreamMeta
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	callToID map[string]string
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		callToID: make(map[string]string),
	}
}

func (m *Manager) Create(callID string) *Session {
	return m.CreateWithParams(CreateParams{CallID: callID})
}

func (m *Manager) CreateWithParams(params CreateParams) *Session {
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	if existingID, ok := m.callToID[params.CallID]; ok {
		existing := m.sessions[existingID]
		if existing != nil {
			existing.UpdatedAt = now
			if params.Caller != "" {
				existing.Caller = params.Caller
			}
			if params.Stream.Encoding != "" {
				existing.Stream = params.Stream
			}
			if params.Providers != (ProviderBindings{}) {
				existing.Providers = params.Providers
			}
			return existing.Clone()
		}
	}

	session := &Session{
		ID:        "sess_" + uuid.NewString(),
		CallID:    params.CallID,
		Caller:    params.Caller,
		State:     StateIdle,
		StartedAt: now,
		UpdatedAt: now,
		Providers: params.Providers,
		Stream:    params.Stream,
	}

	m.sessions[session.ID] = session
	if params.CallID != "" {
		m.callToID[params.CallID] = session.ID
	}

	return session.Clone()
}

func (m *Manager) Get(lookup string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.resolveLocked(lookup)
	if !ok {
		return nil, false
	}

	return session.Clone(), true
}

func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	items := make([]*Session, 0, len(m.sessions))
	for _, current := range m.sessions {
		items = append(items, current.Clone())
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].StartedAt.After(items[j].StartedAt)
	})

	return items
}

func (m *Manager) Update(lookup string, mutate func(*Session) error) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, ok := m.resolveLocked(lookup)
	if !ok {
		return nil, ErrSessionNotFound
	}

	working := current.Clone()
	if err := mutate(working); err != nil {
		return nil, err
	}

	working.UpdatedAt = time.Now().UTC()
	m.sessions[current.ID] = working
	if working.CallID != "" {
		m.callToID[working.CallID] = working.ID
	}

	return working.Clone(), nil
}

func (m *Manager) Close(lookup string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, ok := m.resolveLocked(lookup)
	if !ok {
		return nil, false
	}

	now := time.Now().UTC()
	current.State = StateClosed
	current.UpdatedAt = now
	current.ClosedAt = &now

	closed := current.Clone()
	delete(m.sessions, current.ID)
	if current.CallID != "" {
		delete(m.callToID, current.CallID)
	}

	return closed, true
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.sessions)
}

func (m *Manager) resolveLocked(lookup string) (*Session, bool) {
	if direct, ok := m.sessions[lookup]; ok {
		return direct, true
	}

	if sessionID, ok := m.callToID[lookup]; ok {
		if current, ok := m.sessions[sessionID]; ok {
			return current, true
		}
	}

	return nil, false
}
