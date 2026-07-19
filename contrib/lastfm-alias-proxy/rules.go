package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

type albumRule struct {
	ID          string `json:"id" yaml:"id"`
	Album       string `json:"album" yaml:"album"`
	AlbumArtist string `json:"album_artist" yaml:"album_artist"`
}

type ruleConfig struct {
	AlbumRules    []albumRule       `json:"album_rules" yaml:"album_rules"`
	ArtistAliases map[string]string `json:"artist_aliases" yaml:"artist_aliases"`
	UseSortArtist []string          `json:"use_sort_artist" yaml:"use_sort_artist"`
}

func emptyRuleConfig() ruleConfig {
	return ruleConfig{ArtistAliases: map[string]string{}}
}

func (c ruleConfig) summary() string {
	return fmt.Sprintf("%d album rule(s), %d artist alias(es), %d use_sort_artist",
		len(c.AlbumRules), len(c.ArtistAliases), len(c.UseSortArtist))
}

// parseRuleFileBytes accepts YAML (preferred) or JSON.
func parseRuleFileBytes(data []byte) (ruleConfig, error) {
	cfg := emptyRuleConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		cfg = emptyRuleConfig()
		if err2 := json.Unmarshal(data, &cfg); err2 != nil {
			return emptyRuleConfig(), fmt.Errorf("yaml: %v; json: %v", err, err2)
		}
	}
	if cfg.ArtistAliases == nil {
		cfg.ArtistAliases = map[string]string{}
	}
	return cfg, nil
}

func loadRuleConfigFromPath(path string) (ruleConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return emptyRuleConfig(), err
	}
	return parseRuleFileBytes(data)
}

func loadRuleConfig() ruleConfig {
	cfg := emptyRuleConfig()

	// Legacy env/file for artist aliases only.
	for k, v := range loadAliases() {
		cfg.ArtistAliases[k] = v
	}

	path := os.Getenv("RULES_FILE")
	if path == "" {
		return cfg
	}
	fileCfg, err := loadRuleConfigFromPath(path)
	if err != nil {
		log.Printf("warning: could not load RULES_FILE: %v", err)
		return cfg
	}
	mergeRuleConfig(&cfg, fileCfg)
	return cfg
}

func mergeRuleConfig(dst *ruleConfig, src ruleConfig) {
	if src.ArtistAliases != nil {
		for k, v := range src.ArtistAliases {
			dst.ArtistAliases[k] = v
		}
	}
	dst.AlbumRules = append(dst.AlbumRules, src.AlbumRules...)
	dst.UseSortArtist = append(dst.UseSortArtist, src.UseSortArtist...)
}

// loadAliases reads the alias map from ARTIST_ALIASES or ARTIST_ALIASES_FILE.
func loadAliases() map[string]string {
	envJSON := os.Getenv("ARTIST_ALIASES")
	filePath := os.Getenv("ARTIST_ALIASES_FILE")

	var raw []byte
	switch {
	case envJSON != "":
		raw = []byte(envJSON)
	case filePath != "":
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Printf("warning: could not read ARTIST_ALIASES_FILE: %v", err)
			return map[string]string{}
		}
		raw = data
	default:
		return map[string]string{}
	}

	aliases := make(map[string]string)
	if err := json.Unmarshal(raw, &aliases); err != nil {
		log.Printf("warning: invalid artist alias JSON: %v", err)
		return map[string]string{}
	}
	return aliases
}

// ruleStore holds the active rules and supports atomic swap on reload.
type ruleStore struct {
	mu   sync.RWMutex
	cfg  ruleConfig
	path string
}

func newRuleStore(cfg ruleConfig, path string) *ruleStore {
	return &ruleStore{cfg: cfg, path: path}
}

func (s *ruleStore) get() ruleConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *ruleStore) set(cfg ruleConfig) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// reloadFromDisk reloads RULES_FILE (+ legacy env aliases). On failure keeps the old config.
func (s *ruleStore) reloadFromDisk() error {
	cfg := emptyRuleConfig()
	for k, v := range loadAliases() {
		cfg.ArtistAliases[k] = v
	}
	if s.path == "" {
		s.set(cfg)
		return nil
	}
	fileCfg, err := loadRuleConfigFromPath(s.path)
	if err != nil {
		return err
	}
	mergeRuleConfig(&cfg, fileCfg)
	s.set(cfg)
	return nil
}

// watchRulesFile watches the rules file directory and reloads on write/create/rename.
// Bad files log a warning and keep the previous config.
func watchRulesFile(store *ruleStore) {
	if store.path == "" {
		return
	}
	abs, err := filepath.Abs(store.path)
	if err != nil {
		log.Printf("warning: rules watch disabled: %v", err)
		return
	}
	dir := filepath.Dir(abs)
	base := filepath.Base(abs)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("warning: rules watch disabled: %v", err)
		return
	}

	go func() {
		defer watcher.Close()
		var debounce *time.Timer
		var debounceMu sync.Mutex
		trigger := func() {
			debounceMu.Lock()
			defer debounceMu.Unlock()
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(250*time.Millisecond, func() {
				if err := store.reloadFromDisk(); err != nil {
					log.Printf("warning: rules reload failed (keeping previous): %v", err)
					return
				}
				log.Printf("reloaded rules: %s", store.get().summary())
			})
		}

		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				name := filepath.Base(ev.Name)
				// Editors often write via rename/temp files; match basename or ignore unknown.
				if name != base && !strings.HasPrefix(name, base) && !strings.HasSuffix(ev.Name, base) {
					// Also accept events on the exact path.
					if filepath.Clean(ev.Name) != abs {
						continue
					}
				}
				if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) || ev.Has(fsnotify.Rename) || ev.Has(fsnotify.Chmod) {
					trigger()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("warning: rules watcher error: %v", err)
			}
		}
	}()

	if err := watcher.Add(dir); err != nil {
		log.Printf("warning: rules watch disabled (add dir): %v", err)
		_ = watcher.Close()
		return
	}
	log.Printf("watching rules file %s", abs)
}
