package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Server is the HTTP API.
//
// Endpoints (Phase 1):
//   GET  /             health + version
//   GET  /slots        local registry
//   GET  /slot/{id}    one slot manifest
//   GET  /peers        cached peer registry
//   POST /msg          deposit a message into ~/.mesh/inbox/
//   GET  /presence     simple liveness ping
//
// Phase 3 will add: POST /slot/{id}/invoke, GET /job/{id}/stream.
type Server struct {
	addr string
	reg  *Registry
	peer *PeerCache
}

func newServer(addr string, reg *Registry, peer *PeerCache) *Server {
	return &Server{addr: addr, reg: reg, peer: peer}
}

func (s *Server) run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/slots", s.handleSlots)
	mux.HandleFunc("/slot/", s.handleSlot)
	mux.HandleFunc("/peers", s.handlePeers)
	mux.HandleFunc("/msg", s.handleMsg)
	mux.HandleFunc("/presence", s.handlePresence)
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           logging(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"agent":   "mesh-agent",
		"version": Version,
		"slots":   s.reg.count(),
	})
}

func (s *Server) handleSlots(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"slots": s.reg.list()})
}

func (s *Server) handleSlot(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/slot/")
	if id == "" {
		http.Error(w, "missing slot id", 400)
		return
	}
	slot, ok := s.reg.get(id)
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, slot)
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"peers": s.peer.list()})
}

func (s *Server) handleMsg(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// Minimal payload validation: must be valid JSON with from/to/body.
	var msg struct {
		From string `json:"from"`
		To   string `json:"to"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "expected JSON {from,to,body}", 400)
		return
	}
	stamp := time.Now().UTC().Format("20060102T150405.000Z")
	fname := fmt.Sprintf("%s/%s_%s.json", inboxDir(), stamp, sanitize(msg.From))
	if err := os.WriteFile(fname, body, 0o644); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 202, map[string]any{"queued": fname})
}

func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"name":      hostname(),
		"now":       time.Now().UTC().Format(time.RFC3339),
		"slots":    s.reg.count(),
		"version":   Version,
	})
}

// ── helpers ───────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "anon"
	}
	return string(out)
}

func hostname() string {
	n, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return n
}

// resolveBind picks an address to listen on.
// If bind is "" we try to pick the first tailnet IPv4 (100.x.x.x).
// If none found, we fall back to 0.0.0.0 (only safe inside a Tailscale-only ACL).
func resolveBind(bind string, port int) (string, error) {
	if bind != "" {
		return fmt.Sprintf("%s:%d", bind, port), nil
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil {
			s := ip.IP.String()
			if strings.HasPrefix(s, "100.") {
				return fmt.Sprintf("%s:%d", s, port), nil
			}
		}
	}
	return fmt.Sprintf("0.0.0.0:%d", port), nil
}

// logging is a thin access-log middleware.
func logging(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		ww := &statusWriter{ResponseWriter: w, code: 200}
		h.ServeHTTP(ww, r)
		fmt.Printf("%s %s %d %s %s\n",
			r.Method, r.URL.Path, ww.code,
			time.Since(t0).Round(time.Millisecond), r.RemoteAddr)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}
