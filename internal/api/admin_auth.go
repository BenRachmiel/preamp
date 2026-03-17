package api

import (
	"context"
	"net/http"
)

// adminAuthMiddleware enforces trusted-header auth for the admin API.
// In production, only the SPA container can reach the admin port (network-isolated),
// and the gateway (Authelia) sets Remote-User. In dev mode (AuthDisabled), any
// request is accepted with a fallback username of "dev".
func (s *Server) adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := r.Header.Get("Remote-User")
		if u == "" {
			u = r.Header.Get("X-Forwarded-User")
		}

		if s.cfg.AuthDisabled {
			if u == "" {
				u = "dev"
			}
			r = r.WithContext(context.WithValue(r.Context(), usernameKey, u))
			next.ServeHTTP(w, r)
			return
		}

		if u == "" {
			http.Error(w, `{"error":"missing Remote-User header"}`, http.StatusUnauthorized)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), usernameKey, u))
		next.ServeHTTP(w, r)
	})
}
