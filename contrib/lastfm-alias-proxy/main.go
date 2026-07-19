package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	secret := os.Getenv("LASTFM_API_SECRET")
	rulesPath := os.Getenv("RULES_FILE")
	cfg := loadRuleConfig()

	if secret == "" {
		log.Print("warning: LASTFM_API_SECRET not set; rewriting will be disabled")
	}
	log.Printf("loaded %s", cfg.summary())

	store := newRuleStore(cfg, rulesPath)
	watchRulesFile(store)

	p := newProxy(secret, store)
	mux := http.NewServeMux()
	registerRoutes(mux, p)

	addr := listenAddr()
	log.Printf("listening on %s, proxying to %s", addr, upstreamBaseURL)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
