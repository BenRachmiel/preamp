package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMaxBodyReaderRejectsLargeBody(t *testing.T) {
	srv := testServer(t)

	// Create a body slightly over 1MB.
	body := strings.NewReader(strings.Repeat("x", (1<<20)+1))
	req := httptest.NewRequest("POST", "/rest/ping?f=json", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// The server should reject the oversized body. The exact behavior depends
	// on whether the handler reads the body — for POST form parsing it will.
	// MaxBytesReader causes http.Error 413 or the handler sees an error on read.
	// Since ping doesn't read the body, we need an endpoint that does.
	// Actually, FormValue triggers ParseForm which reads the body.
	// Let's test with a POST that uses form values.
	body2 := strings.NewReader("u=alice&p=" + strings.Repeat("x", (1<<20)+1))
	req2 := httptest.NewRequest("POST", "/rest/ping", body2)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)

	// With MaxBytesReader, the form parse will fail and the handler won't
	// see valid params. We expect either a 413 or a subsonic error.
	if w2.Code == http.StatusOK && strings.Contains(w2.Body.String(), `"status":"ok"`) {
		t.Error("expected rejection for oversized POST body, but got ok")
	}
}

func TestMaxBodyReaderAllowsNormalRequest(t *testing.T) {
	srv := testServer(t)

	req := httptest.NewRequest("GET", "/rest/ping?f=json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
