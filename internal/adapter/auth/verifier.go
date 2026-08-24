// Package auth validates JWTs via JWKS -- signature, expiry, audience, and
// issuer -- and never issues or signs one. Off-the-shelf only: the mock
// OIDC issuer is a drop-in for a hosted IdP by changing OIDCIssuerURL, no
// code change (CLAUDE.md's Auth section).
package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
)

// Verifier validates access tokens against one OIDC issuer's JWKS.
type Verifier struct {
	verifier *oidc.IDTokenVerifier
}

// New performs OIDC discovery against issuerURL and builds a verifier that
// requires the given audience -- ClientID is set explicitly (never
// SkipClientIDCheck) so a token minted for a different service is rejected,
// not silently accepted. Discovery happens here, at construction, so a
// misconfigured issuer fails boot instead of the first request.
func New(ctx context.Context, issuerURL, audience string) (*Verifier, error) {
	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("auth: discovering issuer %q: %w", issuerURL, err)
	}

	return &Verifier{
		verifier: provider.Verifier(&oidc.Config{ClientID: audience}),
	}, nil
}

type tokenClaims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
}

// Verify checks signature, expiry, audience, and issuer via go-oidc, then
// requires sub/email/name to be present. Every failure is reported through
// one of domain/user's two sentinels (ErrTokenInvalid / ErrMissingClaims)
// so callers -- including http/middleware, which never imports this
// package directly -- can distinguish them without inspecting error
// internals.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (user.Claims, error) {
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return user.Claims{}, fmt.Errorf("%w: %w", user.ErrTokenInvalid, err)
	}

	var tc tokenClaims
	if err := idToken.Claims(&tc); err != nil {
		return user.Claims{}, fmt.Errorf("%w: parsing claims: %w", user.ErrTokenInvalid, err)
	}

	if tc.Subject == "" || tc.Email == "" || tc.Name == "" {
		return user.Claims{}, user.ErrMissingClaims
	}

	return user.Claims{Subject: tc.Subject, Email: tc.Email, Name: tc.Name}, nil
}
