// Package simserver is the in-process Telebirr/Chapa-style simulator HTTP API.
package simserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/hosama-adem/twofold/backend/internal/provider"
)

// Config wires the simulator.
type Config struct {
	Name         string
	HMACSecret   string
	CallbackURL  string
	DefaultDelay time.Duration // used by ScenarioDelay
	HTTPClient   *http.Client
	Log          *slog.Logger
	Now          func() time.Time
}

// Server is the fake payment provider.
type Server struct {
	cfg   Config
	mu    sync.Mutex
	byRef map[string]*payment
	log   *slog.Logger
	http  *http.Client
	now   func() time.Time
}

type payment struct {
	ProviderRef string
	Kind        string
	Status      provider.Status
	AmountMinor int64
	Currency    string
	Reference   string
	PhoneE164   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Scenario    provider.Scenario
}

// New builds a simulator server.
func New(cfg Config) *Server {
	if cfg.Name == "" {
		cfg.Name = "simulator"
	}
	if cfg.DefaultDelay <= 0 {
		cfg.DefaultDelay = 2 * time.Second
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		cfg:   cfg,
		byRef: make(map[string]*payment),
		log:   log,
		http:  hc,
		now:   now,
	}
}

// Handler returns the HTTP mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "twofold-simulator"})
	})
	mux.HandleFunc("POST /v1/collections", s.handleCollection)
	mux.HandleFunc("POST /v1/payouts", s.handlePayout)
	mux.HandleFunc("GET /v1/payments/{ref}", s.handleGet)
	return mux
}

func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request) {
	var req provider.CollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	p := s.create("collection", req.AmountMinor, req.Currency, req.Reference, req.PhoneE164)
	writeJSON(w, http.StatusCreated, provider.CollectionRef{
		ProviderRef: p.ProviderRef,
		Status:      p.Status,
		CreatedAt:   p.CreatedAt,
	})
	s.schedule(p)
}

func (s *Server) handlePayout(w http.ResponseWriter, r *http.Request) {
	var req provider.PayoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	p := s.create("payout", req.AmountMinor, req.Currency, req.Reference, req.PhoneE164)
	writeJSON(w, http.StatusCreated, provider.PayoutRef{
		ProviderRef: p.ProviderRef,
		Status:      p.Status,
		CreatedAt:   p.CreatedAt,
	})
	s.schedule(p)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	s.mu.Lock()
	p, ok := s.byRef[ref]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, provider.PaymentStatus{
		ProviderRef: p.ProviderRef,
		Kind:        p.Kind,
		Status:      p.Status,
		AmountMinor: p.AmountMinor,
		Currency:    p.Currency,
		Reference:   p.Reference,
		UpdatedAt:   p.UpdatedAt,
	})
}

func (s *Server) create(kind string, amount int64, currency, reference, phone string) *payment {
	now := s.now().UTC()
	id := "sim_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	p := &payment{
		ProviderRef: id,
		Kind:        kind,
		Status:      provider.StatusPending,
		AmountMinor: amount,
		Currency:    currency,
		Reference:   reference,
		PhoneE164:   phone,
		CreatedAt:   now,
		UpdatedAt:   now,
		Scenario:    provider.ScenarioForAmount(amount),
	}
	s.mu.Lock()
	s.byRef[id] = p
	s.mu.Unlock()
	return p
}

func (s *Server) schedule(p *payment) {
	switch p.Scenario {
	case provider.ScenarioSilent:
		return
	case provider.ScenarioDecline:
		go s.finalize(p.ProviderRef, provider.StatusFailed, 50*time.Millisecond, false)
	case provider.ScenarioDelay:
		go s.finalize(p.ProviderRef, provider.StatusSucceeded, s.cfg.DefaultDelay, false)
	case provider.ScenarioDuplicate:
		go s.finalize(p.ProviderRef, provider.StatusSucceeded, 50*time.Millisecond, true)
	default:
		go s.finalize(p.ProviderRef, provider.StatusSucceeded, 50*time.Millisecond, false)
	}
}

func (s *Server) finalize(ref string, status provider.Status, wait time.Duration, duplicate bool) {
	if wait > 0 {
		time.Sleep(wait)
	}
	s.mu.Lock()
	p, ok := s.byRef[ref]
	if !ok {
		s.mu.Unlock()
		return
	}
	p.Status = status
	p.UpdatedAt = s.now().UTC()
	snap := *p
	s.mu.Unlock()

	evt := provider.WebhookEvent{
		EventID:     "evt_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Provider:    s.cfg.Name,
		ProviderRef: snap.ProviderRef,
		Kind:        snap.Kind,
		Status:      status,
		AmountMinor: snap.AmountMinor,
		Currency:    snap.Currency,
		Reference:   snap.Reference,
		OccurredAt:  snap.UpdatedAt,
	}
	s.postWebhook(evt)
	if duplicate {
		// Same event_id again — Day 23 dedupes on (provider, event_id).
		s.postWebhook(evt)
	}
}

func (s *Server) postWebhook(evt provider.WebhookEvent) {
	if strings.TrimSpace(s.cfg.CallbackURL) == "" {
		s.log.Info("simulator webhook skipped (no CALLBACK_URL)", "event_id", evt.EventID, "status", evt.Status)
		return
	}
	body, err := json.Marshal(evt)
	if err != nil {
		s.log.Error("simulator marshal webhook", "error", err)
		return
	}
	now := s.now().UTC()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.cfg.CallbackURL, bytes.NewReader(body))
	if err != nil {
		s.log.Error("simulator webhook request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(provider.HeaderSignature, provider.SignWebhook(s.cfg.HMACSecret, now, body))
	res, err := s.http.Do(req)
	if err != nil {
		s.log.Warn("simulator webhook delivery failed", "error", err, "event_id", evt.EventID)
		return
	}
	defer res.Body.Close()
	s.log.Info("simulator webhook delivered",
		"event_id", evt.EventID,
		"status", evt.Status,
		"http_status", res.StatusCode,
	)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
