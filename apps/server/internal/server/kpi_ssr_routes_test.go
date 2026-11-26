package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestKPISSRRoutesMountedWhenHTMLViewEnabled(t *testing.T) {
	dummyPool := new(pgxpool.Pool)
	router := Build(Deps{
		Logger:          slog.New(slog.DiscardHandler),
		Pool:            dummyPool,
		HTMLViewEnabled: true,
	})

	mounted := map[string]bool{}
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		mounted[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}

	for _, want := range []string{
		"GET /kpi",
		"GET /kpi/prs",
		"GET /kpi/failures/{id}",
	} {
		if !mounted[want] {
			t.Fatalf("expected SSR route %s to be registered when HTMLViewEnabled=true", want)
		}
	}

	// Verify invalid query/UUID returns 400 from SSR handler (not SPA 200/404 fallback).
	for _, path := range []string{
		"/kpi?window=99d",
		"/kpi?page=0",
		"/kpi/prs?sort=invalid_sort",
		"/kpi/failures/not-a-uuid",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s: status = %d, want 400", path, rec.Code)
		}
	}

	// Verify disabled HTMLViewEnabled does not register /kpi SSR routes.
	disabledRouter := Build(Deps{
		Logger:          slog.New(slog.DiscardHandler),
		Pool:            dummyPool,
		HTMLViewEnabled: false,
	})
	disabledMounted := map[string]bool{}
	if err := chi.Walk(disabledRouter, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		disabledMounted[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatalf("chi.Walk disabled: %v", err)
	}
	for _, route := range []string{"GET /kpi", "GET /kpi/prs", "GET /kpi/failures/{id}"} {
		if disabledMounted[route] {
			t.Fatalf("route %s should not be registered when HTMLViewEnabled=false", route)
		}
	}
}
