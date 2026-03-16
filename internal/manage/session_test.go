package manage

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionCreateGetDelete(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	id, cookie := store.Create("alice")
	if id == "" {
		t.Fatal("empty session ID")
	}
	if cookie.Name != sessionCookieName {
		t.Errorf("cookie name = %q, want %q", cookie.Name, sessionCookieName)
	}
	if cookie.Path != "/manage/" {
		t.Errorf("cookie path = %q, want /manage/", cookie.Path)
	}
	if !cookie.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}

	// Get with valid cookie.
	req := httptest.NewRequest("GET", "/manage/", nil)
	req.AddCookie(cookie)
	sess, ok := store.Get(req)
	if !ok {
		t.Fatal("expected session to be found")
	}
	if sess.Username != "alice" {
		t.Errorf("username = %q, want alice", sess.Username)
	}

	// Delete and verify gone.
	store.Delete(id)
	_, ok = store.Get(req)
	if ok {
		t.Error("session should be deleted")
	}
}

func TestSessionExpiry(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	id, cookie := store.Create("bob")

	// Manually expire it.
	store.mu.Lock()
	store.sessions[id].ExpiresAt = time.Now().Add(-1 * time.Second)
	store.mu.Unlock()

	req := httptest.NewRequest("GET", "/manage/", nil)
	req.AddCookie(cookie)
	_, ok := store.Get(req)
	if ok {
		t.Error("expired session should not be returned")
	}
}

func TestSessionReap(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	id, _ := store.Create("charlie")

	// Expire and reap.
	store.mu.Lock()
	store.sessions[id].ExpiresAt = time.Now().Add(-1 * time.Second)
	store.mu.Unlock()

	store.reap()

	store.mu.RLock()
	_, exists := store.sessions[id]
	store.mu.RUnlock()
	if exists {
		t.Error("reaped session should be removed from map")
	}
}

func TestSessionNoCookie(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	req := httptest.NewRequest("GET", "/manage/", nil)
	_, ok := store.Get(req)
	if ok {
		t.Error("should return false with no cookie")
	}
}

func TestSessionInvalidCookie(t *testing.T) {
	store := NewSessionStore()
	defer store.Close()

	req := httptest.NewRequest("GET", "/manage/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "nonexistent"})
	_, ok := store.Get(req)
	if ok {
		t.Error("should return false for unknown session ID")
	}
}
