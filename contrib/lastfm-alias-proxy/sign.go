package main

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	upstreamBaseURL = "https://ws.audioscrobbler.com/2.0/"

	methodUpdateNowPlaying = "track.updateNowPlaying"
	methodScrobble         = "track.scrobble"

	paramNDSortArtist      = "nd_sortArtist"
	paramNDSortAlbumArtist = "nd_sortAlbumArtist"
)

var (
	rewriteMethods = []string{methodUpdateNowPlaying, methodScrobble}
	indexedKey     = regexp.MustCompile(`^([a-zA-Z_]+)\[(\d+)\]$`)
)

// computeAPISig returns the Last.fm api_sig for the given parameters.
// Signing rules match adapters/lastfm/client.go: exclude format and callback,
// sort keys alphabetically, concatenate key+firstValue for each, append secret.
func computeAPISig(params url.Values, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "format" || k == "callback" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var msg strings.Builder
	for _, k := range keys {
		msg.WriteString(k)
		msg.WriteString(params[k][0])
	}
	msg.WriteString(secret)

	hash := md5.Sum([]byte(msg.String()))
	return hex.EncodeToString(hash[:])
}

func resignParams(params url.Values, secret string) {
	params.Del("api_sig")
	params.Set("api_sig", computeAPISig(params, secret))
}

func shouldRewrite(method string) bool {
	return slices.Contains(rewriteMethods, method)
}

func normaliseKey(s string) string {
	s = strings.TrimSpace(s)
	s = norm.NFC.String(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
}

func fieldBaseAndIndex(key string) (base, index string) {
	if m := indexedKey.FindStringSubmatch(key); m != nil {
		return m[1], m[2]
	}
	return key, ""
}

// rewriteArtistFields applies exact-match alias lookups to artist and albumArtist
// (including indexed batch keys). Returns true when at least one field was rewritten.
func rewriteArtistFields(params url.Values, aliases map[string]string) bool {
	if len(aliases) == 0 {
		return false
	}
	lookup := make(map[string]string, len(aliases))
	for k, v := range aliases {
		lookup[normaliseKey(k)] = v
	}

	rewritten := false
	for key, values := range params {
		base, _ := fieldBaseAndIndex(key)
		if base != "artist" && base != "albumArtist" {
			continue
		}
		if len(values) == 0 {
			continue
		}
		if alias, found := lookup[normaliseKey(values[0])]; found {
			params.Set(key, alias)
			rewritten = true
		}
	}
	return rewritten
}

// rewriteSortArtists replaces artist/albumArtist with nd_sort* when the display
// name is on the allow-list and a non-empty sort value is present.
func rewriteSortArtists(params url.Values, allowlist []string) bool {
	if len(allowlist) == 0 {
		return false
	}
	allowed := make(map[string]struct{}, len(allowlist))
	for _, name := range allowlist {
		allowed[normaliseKey(name)] = struct{}{}
	}

	changed := false
	for key, values := range params {
		base, idx := fieldBaseAndIndex(key)
		if base != "artist" && base != "albumArtist" {
			continue
		}
		if len(values) == 0 {
			continue
		}
		if _, ok := allowed[normaliseKey(values[0])]; !ok {
			continue
		}
		sortParam := paramNDSortArtist
		if base == "albumArtist" {
			sortParam = paramNDSortAlbumArtist
		}
		if idx != "" {
			sortParam = sortParam + "[" + idx + "]"
		}
		sortVal := strings.TrimSpace(params.Get(sortParam))
		if sortVal == "" {
			continue
		}
		params.Set(key, sortVal)
		changed = true
	}
	return changed
}

// rewriteAlbumArtists applies album allow-list rules. Only albumArtist fields change.
func rewriteAlbumArtists(params url.Values, rules []albumRule) (changed bool, ruleID string) {
	if len(rules) == 0 {
		return false, ""
	}

	albumsByIndex := map[string]string{} // "" for plain album, "0" for album[0]
	for key, values := range params {
		if len(values) == 0 {
			continue
		}
		if key == "album" {
			albumsByIndex[""] = values[0]
			continue
		}
		base, idx := fieldBaseAndIndex(key)
		if base == "album" {
			albumsByIndex[idx] = values[0]
		}
	}

	for _, rule := range rules {
		want := normaliseKey(rule.Album)
		for idx, album := range albumsByIndex {
			if normaliseKey(album) != want {
				continue
			}
			artistKey := "albumArtist"
			if idx != "" {
				artistKey = "albumArtist[" + idx + "]"
			}
			// Set even if missing so Last.fm receives the cast name.
			params.Set(artistKey, rule.AlbumArtist)
			changed = true
			ruleID = rule.ID
		}
	}
	return changed, ruleID
}

// stripNDParams removes Navidrome-private helper params (nd_*) before upstream.
func stripNDParams(params url.Values) bool {
	removed := false
	for key := range params {
		base, _ := fieldBaseAndIndex(key)
		if strings.HasPrefix(base, "nd_") {
			params.Del(key)
			removed = true
		}
	}
	return removed
}

func cloneValues(v url.Values) url.Values {
	c := make(url.Values, len(v))
	for k, vals := range v {
		cp := make([]string, len(vals))
		copy(cp, vals)
		c[k] = cp
	}
	return c
}

func parseRequestParams(method string, query url.Values, body []byte) (url.Values, error) {
	if method == "POST" {
		return url.ParseQuery(string(body))
	}
	return cloneValues(query), nil
}

func listenAddr() string {
	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		return addr
	}
	return ":8080"
}