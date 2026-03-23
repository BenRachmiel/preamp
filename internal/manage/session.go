package manage

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookieName = "preamp_session"
	sessionTTL        = 24 * time.Hour
	reapInterval      = 5 * time.Minute
)

// Session represents an authenticated management UI session.
type Session struct {
	ID        string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionStore manages in-memory sessions with automatic expiry.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	stop     chan struct{}
}

// NewSessionStore creates a store and starts the background reaper.
func NewSessionStore() *SessionStore {
	s := &SessionStore{
		sessions: make(map[string]*Session),
		stop:     make(chan struct{}),
	}
	go s.reapLoop()
	return s
}

// Close stops the background reaper.
func (s *SessionStore) Close() {
	close(s.stop)
}

// Create mints a new session and returns the ID and a cookie to set.
func (s *SessionStore) Create(username string) (string, *http.Cookie) {
	id := randomHex(32)
	now := time.Now()
	sess := &Session{
		ID:        id,
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	}

	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/manage/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	}
	return id, cookie
}

// Get reads the session cookie from the request and returns the session if valid.
func (s *SessionStore) Get(r *http.Request) (*Session, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, false
	}

	s.mu.RLock()
	sess, ok := s.sessions[c.Value]
	s.mu.RUnlock()

	if !ok || time.Now().After(sess.ExpiresAt) {
		return nil, false
	}
	return sess, true
}

// Delete removes a session by ID.
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func (s *SessionStore) reapLoop() {
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.reap()
		}
	}
}

func (s *SessionStore) reap() {
	now := time.Now()
	s.mu.Lock()
	for id, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
