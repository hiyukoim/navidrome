package main

import (
	"crypto/md5"
	"fmt"
	"net/url"
	"os"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestRewriteArtistFields(t *testing.T) {
	aliases := map[string]string{
		"Example Artist": "Example Alias",
	}

	t.Run("rewrites artist and albumArtist", func(t *testing.T) {
		params := url.Values{}
		params.Set("artist", "  Example Artist  ")
		params.Set("albumArtist", "Example Artist")
		params.Set("track", "Song")

		if !rewriteArtistFields(params, aliases) {
			t.Fatal("expected rewrite")
		}
		if got := params.Get("artist"); got != "Example Alias" {
			t.Fatalf("artist = %q", got)
		}
		if got := params.Get("albumArtist"); got != "Example Alias" {
			t.Fatalf("albumArtist = %q", got)
		}
	})

	t.Run("no match leaves values unchanged", func(t *testing.T) {
		params := url.Values{}
		params.Set("artist", "Another Artist")
		if rewriteArtistFields(params, aliases) {
			t.Fatal("expected no rewrite")
		}
	})
}

func TestRewriteAlbumArtists(t *testing.T) {
	rules := []albumRule{{
		ID:          "example-ost",
		Album:       "Example Soundtrack",
		AlbumArtist: "Example Cast",
	}}

	t.Run("sets albumArtist only", func(t *testing.T) {
		params := url.Values{}
		params.Set("method", "track.scrobble")
		params.Set("album", "Example Soundtrack")
		params.Set("artist", "Track Performer")
		params.Set("albumArtist", "Wrong Name")

		changed, id := rewriteAlbumArtists(params, rules)
		if !changed || id != "example-ost" {
			t.Fatalf("changed=%v id=%q", changed, id)
		}
		if params.Get("artist") != "Track Performer" {
			t.Fatalf("artist changed: %q", params.Get("artist"))
		}
		if params.Get("albumArtist") != "Example Cast" {
			t.Fatalf("albumArtist = %q", params.Get("albumArtist"))
		}
	})

	t.Run("batch only matching index", func(t *testing.T) {
		params := url.Values{}
		params.Set("album[0]", "Example Soundtrack")
		params.Set("artist[0]", "A")
		params.Set("albumArtist[0]", "Old")
		params.Set("album[1]", "Other Album")
		params.Set("artist[1]", "B")
		params.Set("albumArtist[1]", "Keep")

		changed, _ := rewriteAlbumArtists(params, rules)
		if !changed {
			t.Fatal("expected change")
		}
		if params.Get("albumArtist[0]") != "Example Cast" {
			t.Fatalf("albumArtist[0] = %q", params.Get("albumArtist[0]"))
		}
		if params.Get("albumArtist[1]") != "Keep" {
			t.Fatalf("albumArtist[1] = %q", params.Get("albumArtist[1]"))
		}
		if params.Get("artist[0]") != "A" || params.Get("artist[1]") != "B" {
			t.Fatal("artists changed")
		}
	})

	t.Run("unicode nfc match", func(t *testing.T) {
		// Cafe with combining acute vs precomposed
		decomposed := norm.NFD.String("Café Soundtrack")
		rulesNFC := []albumRule{{ID: "nfc", Album: "Café Soundtrack", AlbumArtist: "Cast"}}
		params := url.Values{}
		params.Set("album", decomposed)
		params.Set("artist", "X")
		changed, _ := rewriteAlbumArtists(params, rulesNFC)
		if !changed {
			t.Fatal("expected NFC match")
		}
		if params.Get("albumArtist") != "Cast" {
			t.Fatalf("albumArtist = %q", params.Get("albumArtist"))
		}
	})
}

func TestLoadAliasesInvalidJSON(t *testing.T) {
	t.Setenv("ARTIST_ALIASES", `{not valid json`)
	t.Setenv("ARTIST_ALIASES_FILE", "")
	t.Setenv("RULES_FILE", "")
	aliases := loadAliases()
	if len(aliases) != 0 {
		t.Fatalf("expected empty map, got %v", aliases)
	}
}

func TestLoadRuleConfigFromFile(t *testing.T) {
	t.Setenv("ARTIST_ALIASES", "")
	t.Setenv("ARTIST_ALIASES_FILE", "")
	file := t.TempDir() + "/rules.json"
	content := `{"album_rules":[{"id":"a","album":"Alb","album_artist":"Cast"}],"artist_aliases":{"A":"B"}}`
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RULES_FILE", file)
	cfg := loadRuleConfig()
	if len(cfg.AlbumRules) != 1 || cfg.AlbumRules[0].AlbumArtist != "Cast" {
		t.Fatalf("album rules: %+v", cfg.AlbumRules)
	}
	if cfg.ArtistAliases["A"] != "B" {
		t.Fatalf("aliases: %+v", cfg.ArtistAliases)
	}
}

func TestLoadRuleConfigYAML(t *testing.T) {
	t.Setenv("ARTIST_ALIASES", "")
	t.Setenv("ARTIST_ALIASES_FILE", "")
	file := t.TempDir() + "/rules.yaml"
	content := `
artist_aliases:
  Example Artist: Example Alias
use_sort_artist:
  - Sort Me
album_rules:
  - id: ost
    album: Example Soundtrack
    album_artist: Example Cast
`
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RULES_FILE", file)
	cfg := loadRuleConfig()
	if cfg.ArtistAliases["Example Artist"] != "Example Alias" {
		t.Fatalf("aliases: %+v", cfg.ArtistAliases)
	}
	if len(cfg.UseSortArtist) != 1 || cfg.UseSortArtist[0] != "Sort Me" {
		t.Fatalf("use_sort_artist: %+v", cfg.UseSortArtist)
	}
	if len(cfg.AlbumRules) != 1 || cfg.AlbumRules[0].AlbumArtist != "Example Cast" {
		t.Fatalf("album rules: %+v", cfg.AlbumRules)
	}
}

func TestRewriteSortArtists(t *testing.T) {
	params := url.Values{}
	params.Set("artist", "Display")
	params.Set("albumArtist", "Display")
	params.Set("nd_sortArtist", "Sorted")
	params.Set("nd_sortAlbumArtist", "Sorted AA")
	if !rewriteSortArtists(params, []string{"Display"}) {
		t.Fatal("expected change")
	}
	if params.Get("artist") != "Sorted" || params.Get("albumArtist") != "Sorted AA" {
		t.Fatalf("got artist=%q albumArtist=%q", params.Get("artist"), params.Get("albumArtist"))
	}
}

func TestAliasOverridesSort(t *testing.T) {
	p := newProxy("SECRET", newRuleStore(ruleConfig{
		UseSortArtist: []string{"Display"},
		ArtistAliases: map[string]string{"Display": "Alias"},
	}, ""))
	params := url.Values{}
	params.Set("method", "track.scrobble")
	params.Set("artist", "Display")
	params.Set("nd_sortArtist", "Sorted")
	params.Set("api_key", "k")
	params.Set("api_sig", "old")
	if !p.applyRewrite(params) {
		t.Fatal("expected rewrite")
	}
	if params.Get("artist") != "Alias" {
		t.Fatalf("artist = %q", params.Get("artist"))
	}
	if params.Get("nd_sortArtist") != "" {
		t.Fatal("nd_sortArtist should be stripped")
	}
}

func TestResignStripsOldAPISig(t *testing.T) {
	secret := "SECRET"
	params := url.Values{}
	params.Add("d", "444")
	params.Add("callback", "https://example.com")
	params.Add("a", "111")
	params.Add("format", "json")
	params.Add("c", "333")
	params.Add("b", "222")
	params.Set("api_sig", "stale-signature")

	resignParams(params, secret)
	expected := fmt.Sprintf("%x", md5.Sum([]byte("a111b222c333d444SECRET")))
	if got := params.Get("api_sig"); got != expected {
		t.Fatalf("api_sig = %q, want %q", got, expected)
	}
}

func TestApplyRewriteAlbumThenResign(t *testing.T) {
	p := newProxy("SECRET", newRuleStore(ruleConfig{
		AlbumRules: []albumRule{{ID: "ex", Album: "Example Soundtrack", AlbumArtist: "Example Cast"}},
	}, ""))
	params := url.Values{}
	params.Set("method", "track.scrobble")
	params.Set("album", "Example Soundtrack")
	params.Set("artist", "Performer")
	params.Set("albumArtist", "Old")
	params.Set("api_key", "key")
	params.Set("api_sig", "stale")

	if !p.applyRewrite(params) {
		t.Fatal("expected rewrite")
	}
	if params.Get("artist") != "Performer" {
		t.Fatal("artist must stay")
	}
	if params.Get("albumArtist") != "Example Cast" {
		t.Fatalf("albumArtist = %q", params.Get("albumArtist"))
	}
	without := cloneValues(params)
	without.Del("api_sig")
	want := computeAPISig(without, "SECRET")
	if params.Get("api_sig") != want {
		t.Fatalf("bad api_sig")
	}
}

func TestNonScrobbleMethodDoesNotRewrite(t *testing.T) {
	p := newProxy("SECRET", newRuleStore(ruleConfig{
		AlbumRules: []albumRule{{ID: "ex", Album: "Example Soundtrack", AlbumArtist: "Example Cast"}},
	}, ""))
	params := url.Values{}
	params.Set("method", "artist.getInfo")
	params.Set("album", "Example Soundtrack")
	params.Set("albumArtist", "Old")
	params.Set("api_sig", "original")
	p.applyRewrite(params)
	if params.Get("albumArtist") != "Old" || params.Get("api_sig") != "original" {
		t.Fatal("non-scrobble must pass through")
	}
}
