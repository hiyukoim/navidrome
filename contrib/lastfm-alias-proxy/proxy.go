package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

type proxy struct {
	secret string
	store  *ruleStore
	client *http.Client
}

func newProxy(secret string, store *ruleStore) *proxy {
	return &proxy{
		secret: secret,
		store:  store,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" || r.URL.Path == "/healthz/" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	params, err := parseRequestParams(r.Method, r.URL.Query(), body)
	if err != nil {
		http.Error(w, "failed to parse request parameters", http.StatusBadRequest)
		return
	}

	useModifiedParams := false
	if shouldRewrite(params.Get("method")) {
		useModifiedParams = p.applyRewrite(params)
	}

	upstreamReq, err := p.buildUpstreamRequest(r, params, body, useModifiedParams)
	if err != nil {
		http.Error(w, "failed to build upstream request", http.StatusInternalServerError)
		return
	}

	resp, err := p.client.Do(upstreamReq)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// applyRewrite applies rules, strips nd_* helpers, and re-signs when anything changed.
// Priority: artist_aliases → use_sort_artist → album_rules (albumArtist).
func (p *proxy) applyRewrite(params url.Values) bool {
	if !shouldRewrite(params.Get("method")) {
		return false
	}
	if p.secret == "" {
		log.Printf("warning: LASTFM_API_SECRET not set; skipping rewrite for %q", params.Get("method"))
		return false
	}

	cfg := p.store.get()
	changed := false

	if rewriteArtistFields(params, cfg.ArtistAliases) {
		changed = true
		log.Printf("applied artist alias rule")
	}
	if rewriteSortArtists(params, cfg.UseSortArtist) {
		changed = true
		log.Printf("applied use_sort_artist rule")
	}
	if albumChanged, ruleID := rewriteAlbumArtists(params, cfg.AlbumRules); albumChanged {
		changed = true
		log.Printf("applied album rule id=%s", ruleID)
	}
	if stripNDParams(params) {
		changed = true
	}
	if !changed {
		return false
	}
	resignParams(params, p.secret)
	return true
}

func (p *proxy) buildUpstreamRequest(r *http.Request, params url.Values, originalBody []byte, useParams bool) (*http.Request, error) {
	var bodyReader io.Reader

	var payload []byte
	if r.Method == http.MethodPost {
		payload = originalBody
		if useParams {
			payload = []byte(params.Encode())
		}
		if len(payload) > 0 {
			bodyReader = bytes.NewReader(payload)
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamBaseURL, bodyReader)
	if err != nil {
		return nil, err
	}

	if r.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if len(payload) > 0 {
			req.ContentLength = int64(len(payload))
		}
	} else if useParams {
		req.URL.RawQuery = params.Encode()
	} else {
		req.URL.RawQuery = r.URL.RawQuery
	}

	return req, nil
}

func registerRoutes(mux *http.ServeMux, handler http.Handler) {
	mux.Handle("/", handler)
	mux.Handle("/2.0/", handler)
	mux.Handle("/2.0", handler)
}
