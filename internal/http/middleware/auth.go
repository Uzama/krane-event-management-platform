// Package middleware holds the request-processing chain: auth today,
// authz/idempotency/request-id/recover arrive with the features that need
// them (CLAUDE.md's target layout).
package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	"github.com/Uzama/krane-event-management-platform/internal/http/response"
)

// TokenVerifier is the narrowest seam Auth needs against the JWT verifier --
// matching handler.Pinger's pattern, so http depends on this interface, not
// on adapter/auth directly. Implemented by *adapter/auth.Verifier.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (user.Claims, error)
}

// UserResolver is the narrowest seam Auth needs against the user aggregate.
// Implemented by *domain/user.Service.
type UserResolver interface {
	GetOrCreateBySubject(ctx context.Context, subject, email, name string) (user.User, error)
}

type contextKey int

const userContextKey contextKey = iota

// UserFromContext returns the user attached by Auth, if any.
func UserFromContext(ctx context.Context) (user.User, bool) {
	u, ok := ctx.Value(userContextKey).(user.User)
	return u, ok
}

// ContextWithUser attaches u the same way Auth does.
//
// TEST SEAM -- NOT FOR PRODUCTION USE. Exported only so tests can exercise
// a middleware chained after Auth (e.g. Authz) without running the real
// token-verification chain first. In production, a user's identity must
// come from exactly one place: a validated bearer token, mapped to a users
// row by Auth. No handler, middleware, or other production code may call
// this to attach a user -- doing so would be an authentication bypass.
func ContextWithUser(ctx context.Context, u user.User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// Auth validates the Authorization: Bearer header via verifier, maps the
// token's sub to a users row via users, and attaches that user to the
// request context. Every rejection is a 401 through the standard error
// envelope -- never a 500 -- and every rejection is logged with a `reason`
// field so token_invalid (bad/forged token) is distinguishable from
// missing_claims (issuer misconfigured for our claim contract) at a glance.
func Auth(verifier TokenVerifier, users UserResolver, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				logger.Warn("auth: rejected request", "reason", "missing_header")
				writeUnauthorized(w, logger)
				return
			}

			claims, err := verifier.Verify(r.Context(), token)
			if err != nil {
				logger.Warn("auth: rejected request", "reason", reasonFor(err), "error", err)
				writeUnauthorized(w, logger)
				return
			}

			u, err := users.GetOrCreateBySubject(r.Context(), claims.Subject, claims.Email, claims.Name)
			if err != nil {
				logger.Error("auth: resolving user", "error", err)
				writeUnauthorized(w, logger)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func reasonFor(err error) string {
	if errors.Is(err, user.ErrMissingClaims) {
		return "missing_claims"
	}
	return "token_invalid"
}

func writeUnauthorized(w http.ResponseWriter, logger *slog.Logger) {
	if err := response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid access token", nil); err != nil {
		logger.Error("auth: writing error envelope", "error", err)
	}
}

const bearerPrefix = "Bearer "

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, bearerPrefix) {
		return "", false
	}
	token := strings.TrimPrefix(h, bearerPrefix)
	if token == "" {
		return "", false
	}
	return token, true
}
