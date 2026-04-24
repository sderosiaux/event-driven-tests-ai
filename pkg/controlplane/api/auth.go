package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/sderosiaux/event-driven-tests-ai/pkg/controlplane/storage"
)

// roleRank maps roles to a hierarchy. Higher rank dominates lower.
var roleRank = map[storage.Role]int{
	storage.RoleViewer: 1,
	storage.RoleWorker: 2,
	storage.RoleEditor: 3,
	storage.RoleAdmin:  4,
}

type ctxKey int

const ctxRoleKey ctxKey = 1

// RoleFromContext returns the role established by RequireRole, or empty.
func RoleFromContext(ctx context.Context) storage.Role {
	r, _ := ctx.Value(ctxRoleKey).(storage.Role)
	return r
}

// RequireRole returns a chi-compatible middleware that rejects requests whose
// bearer token does not have at least the required role.
func (a *API) RequireRole(min storage.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := bearerToken(r)
			if tok == "" {
				writeError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			t, err := a.Store.LookupToken(r.Context(), tok)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			if roleRank[t.Role] < roleRank[min] {
				writeError(w, http.StatusForbidden, "insufficient role: have "+string(t.Role)+", need "+string(min))
				return
			}
			ctx := context.WithValue(r.Context(), ctxRoleKey, t.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}
