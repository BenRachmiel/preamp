package api

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPingJSON(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/ping?")

	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	if resp["version"] != APIVersion {
		t.Errorf("version = %v, want %s", resp["version"], APIVersion)
	}
	if resp["type"] != ServerName {
		t.Errorf("type = %v, want %s", resp["type"], ServerName)
	}
}

func TestPingXML(t *testing.T) {
	srv := testServer(t)
	w := get(t, srv, "/rest/ping")

	if ct := w.Header().Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var resp SubsonicResponse
	if err := xml.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want ok", resp.Status)
	}
}

func TestXMLResponseHasNamespace(t *testing.T) {
	srv := testServer(t)
	w := get(t, srv, "/rest/ping")

	body := w.Body.String()
	if !strings.Contains(body, `xmlns="http://subsonic.org/restapi"`) {
		t.Errorf("XML response missing xmlns attribute:\n%s", body)
	}
}

func TestPingViewSuffix(t *testing.T) {
	srv := testServer(t)
	w := get(t, srv, "/rest/ping.view?f=json")
	if w.Code != http.StatusOK {
		t.Errorf("status %d for .view suffix", w.Code)
	}
}

func TestGetLicense(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getLicense?")

	license, ok := resp["license"].(map[string]any)
	if !ok {
		t.Fatalf("missing license in response")
	}
	if license["valid"] != true {
		t.Errorf("license.valid = %v, want true", license["valid"])
	}
}

func TestGetMusicFolders(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getMusicFolders?")

	mf, ok := resp["musicFolders"].(map[string]any)
	if !ok {
		t.Fatalf("missing musicFolders")
	}
	folders, ok := mf["musicFolder"].([]any)
	if !ok || len(folders) == 0 {
		t.Fatalf("missing musicFolder array")
	}
	f := folders[0].(map[string]any)
	if f["name"] != "Music" {
		t.Errorf("folder name = %v", f["name"])
	}
}

func TestGetOpenSubsonicExtensions(t *testing.T) {
	srv := testServer(t)
	resp := getJSON(t, srv, "/rest/getOpenSubsonicExtensions?")

	// Should be present (even if empty).
	if resp["status"] != "ok" {
		t.Errorf("status = %v", resp["status"])
	}
}

func TestPingPOST(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("POST", "/rest/ping?f=json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var wrapper subsonicJSON
	if err := json.Unmarshal(w.Body.Bytes(), &wrapper); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(wrapper.Response, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
}

func TestPingViewSuffixPOST(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("POST", "/rest/ping.view?f=json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d for POST .view suffix", w.Code)
	}
}
