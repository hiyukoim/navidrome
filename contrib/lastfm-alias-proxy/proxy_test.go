package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testProxy(secret string, cfg ruleConfig) *proxy {
	return newProxy(secret, newRuleStore(cfg, ""))
}

func TestProxyHealthz(t *testing.T) {
	p := testProxy("SECRET", ruleConfig{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
		t.Fatalf("healthz: %d %q", rr.Code, rr.Body.String())
	}
}

func TestProxyScrobbleAlbumRewrite(t *testing.T) {
	p := testProxy("SECRET", ruleConfig{
		AlbumRules: []albumRule{{ID: "ex", Album: "Example Soundtrack", AlbumArtist: "Example Cast"}},
	})

	var capturedBody string
	p.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		capturedBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     make(http.Header),
		}, nil
	})}

	form := url.Values{}
	form.Set("method", "track.scrobble")
	form.Set("album", "Example Soundtrack")
	form.Set("artist", "Performer")
	form.Set("albumArtist", "Old")
	form.Set("api_key", "key")
	form.Set("format", "json")
	form.Set("api_sig", "old")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	got, _ := url.ParseQuery(capturedBody)
	if got.Get("artist") != "Performer" {
		t.Fatalf("artist = %q", got.Get("artist"))
	}
	if got.Get("albumArtist") != "Example Cast" {
		t.Fatalf("albumArtist = %q", got.Get("albumArtist"))
	}
	if got.Get("api_sig") == "old" {
		t.Fatal("api_sig not resigned")
	}
	if got.Get("nd_sortArtist") != "" {
		t.Fatal("nd_* must be stripped")
	}
}

func TestProxySortArtistAndAliasPriority(t *testing.T) {
	p := testProxy("SECRET", ruleConfig{
		UseSortArtist: []string{"Display Artist"},
		ArtistAliases: map[string]string{"Display Artist": "Manual Alias"},
	})
	var capturedBody string
	p.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		capturedBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})}

	form := url.Values{}
	form.Set("method", "track.updateNowPlaying")
	form.Set("artist", "Display Artist")
	form.Set("albumArtist", "Display Artist")
	form.Set("nd_sortArtist", "Sort Name")
	form.Set("nd_sortAlbumArtist", "Sort Album")
	form.Set("api_key", "key")
	form.Set("api_sig", "old")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)

	got, _ := url.ParseQuery(capturedBody)
	// Alias overrides sort.
	if got.Get("artist") != "Manual Alias" {
		t.Fatalf("artist = %q, want Manual Alias", got.Get("artist"))
	}
	if got.Get("nd_sortArtist") != "" || got.Get("nd_sortAlbumArtist") != "" {
		t.Fatalf("nd_* leaked: %v", got)
	}
}

func TestProxyUseSortArtistOnly(t *testing.T) {
	p := testProxy("SECRET", ruleConfig{
		UseSortArtist: []string{"Display Artist"},
	})
	var capturedBody string
	p.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		capturedBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})}

	form := url.Values{}
	form.Set("method", "track.scrobble")
	form.Set("artist", "Display Artist")
	form.Set("albumArtist", "Other")
	form.Set("nd_sortArtist", "Sort Name")
	form.Set("nd_sortAlbumArtist", "Ignored")
	form.Set("api_key", "key")
	form.Set("api_sig", "old")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	p.ServeHTTP(httptest.NewRecorder(), req)

	got, _ := url.ParseQuery(capturedBody)
	if got.Get("artist") != "Sort Name" {
		t.Fatalf("artist = %q", got.Get("artist"))
	}
	if got.Get("albumArtist") != "Other" {
		t.Fatalf("albumArtist = %q", got.Get("albumArtist"))
	}
}

func TestProxyArtistGetInfoPassthrough(t *testing.T) {
	p := testProxy("SECRET", ruleConfig{
		AlbumRules: []albumRule{{ID: "ex", Album: "Example Soundtrack", AlbumArtist: "Example Cast"}},
	})
	var captured *http.Request
	p.client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		captured = req
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})}
	query := "method=artist.getInfo&artist=Cher&api_key=key&format=json"
	req := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if captured.URL.RawQuery != query {
		t.Fatalf("query changed: %q", captured.URL.RawQuery)
	}
}

func TestRulesReloadKeepsOldOnBadYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yaml")
	good := "artist_aliases:\n  A: B\n"
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadRuleConfigFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	store := newRuleStore(cfg, path)
	watchRulesFile(store)

	if store.get().ArtistAliases["A"] != "B" {
		t.Fatalf("initial: %+v", store.get())
	}

	if err := os.WriteFile(path, []byte("artist_aliases: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		// Bad reload should keep A→B
		if store.get().ArtistAliases["A"] == "B" {
			// still good; wait a bit more to ensure watcher fired
		}
	}
	time.Sleep(400 * time.Millisecond)
	if store.get().ArtistAliases["A"] != "B" {
		t.Fatalf("bad yaml should keep old rules, got %+v", store.get())
	}

	if err := os.WriteFile(path, []byte("artist_aliases:\n  A: C\nuse_sort_artist:\n  - X\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.get().ArtistAliases["A"] == "C" && len(store.get().UseSortArtist) == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("reload did not apply good yaml: %+v", store.get())
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
