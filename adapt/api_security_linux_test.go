//go:build linux

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPanelSecurityRequiresConfiguredBasicAuth(t *testing.T) {
	t.Setenv("LALUNE_PANEL_PASSWORD", "fixture-secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := panelSecurity(next)

	for _, tc := range []struct {
		name, auth string
		want       int
	}{
		{"missing credentials", "", http.StatusUnauthorized},
		{"wrong credentials", "Basic YWRtaW46d3Jvbmc=", http.StatusUnauthorized},
		{"correct credentials", "Basic YWRtaW46Zml4dHVyZS1zZWNyZXQ=", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://nanopi:1061/", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%q", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") != "" {
				t.Fatal("native browser authentication prompt must never be triggered")
			}
		})
	}
}

func TestPanelSecurityRejectsCrossOriginStateChanges(t *testing.T) {
	t.Setenv("LALUNE_PANEL_PASSWORD", "fixture-secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := panelSecurity(next)

	tests := []struct {
		name, origin string
		want         int
	}{
		{"same origin", "http://nanopi:1061", http.StatusNoContent},
		{"foreign origin", "https://attacker.example", http.StatusForbidden},
		{"missing origin", "", http.StatusForbidden},
		{"origin userinfo", "http://admin@nanopi:1061", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://nanopi:1061/api/settings", nil)
			req.SetBasicAuth("admin", "fixture-secret")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d", rec.Code, tc.want)
			}
			if rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("wildcard CORS header must not be emitted")
			}
		})
	}
}

func TestPanelSecurityFailsClosedWithoutPassword(t *testing.T) {
	t.Setenv("LALUNE_PANEL_PASSWORD", "")
	handler := panelSecurity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("next handler must not run") }))
	req := httptest.NewRequest(http.MethodGet, "http://nanopi:1061/", nil)
	req.SetBasicAuth("admin", "anything")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=503", rec.Code)
	}
}
