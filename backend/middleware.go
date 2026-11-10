package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
)

type contextKey string

const userContextKey contextKey = "user"

func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		u, err := s.User(r.Context(), token)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, u)))
	})
}
func userFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userContextKey).(User)
	return u, ok
}
