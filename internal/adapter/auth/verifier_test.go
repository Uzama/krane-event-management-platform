package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/auth"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
)

const testAudience = "krane-api"

// fakeIssuer is a local, fully-controlled OIDC issuer double: a real
// discovery document + a real JWKS endpoint, backed by a self-signed key.
// Pointing auth.New at it exercises go-oidc's actual verification path --
// these tests prove go-oidc rejects bad tokens, not that our own wrapper
// logic does.
type fakeIssuer struct {
	server *httptest.Server
	priv   *rsa.PrivateKey
	keyID  string
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}

	fi := &fakeIssuer{priv: priv, keyID: "test-key"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", fi.discovery)
	mux.HandleFunc("/jwks", fi.jwks)
	fi.server = httptest.NewServer(mux)
	t.Cleanup(fi.server.Close)

	return fi
}

func (fi *fakeIssuer) discovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                 fi.server.URL,
		"authorization_endpoint": fi.server.URL + "/authorize",
		"token_endpoint":         fi.server.URL + "/token",
		"jwks_uri":               fi.server.URL + "/jwks",
	})
}

func (fi *fakeIssuer) jwks(w http.ResponseWriter, _ *http.Request) {
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key:       &fi.priv.PublicKey,
		KeyID:     fi.keyID,
		Algorithm: string(jose.RS256),
		Use:       "sig",
	}}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(set)
}

// mint signs claims with the issuer's own key -- the "legitimate token"
// path unless a test overrides sub/aud/iss/exp itself.
func (fi *fakeIssuer) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	return mintWithKey(t, fi.priv, fi.keyID, claims)
}

// mintWithWrongKey signs with a key never published in the JWKS -- proves
// signature verification, not just claim-shape checking.
func (fi *fakeIssuer) mintWithWrongKey(t *testing.T, claims map[string]any) string {
	t.Helper()
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating imposter key: %v", err)
	}
	return mintWithKey(t, other, fi.keyID, claims)
}

func mintWithKey(t *testing.T, priv *rsa.PrivateKey, keyID string, claims map[string]any) string {
	t.Helper()

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID),
	)
	if err != nil {
		t.Fatalf("building signer: %v", err)
	}

	builder := josejwt.Signed(signer)
	raw, err := builder.Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return raw
}

func validClaims(fi *fakeIssuer) map[string]any {
	now := time.Now()
	return map[string]any{
		"sub":   "user-123",
		"email": "user@example.com",
		"name":  "Test User",
		"aud":   testAudience,
		"iss":   fi.server.URL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
}

func newVerifier(t *testing.T, fi *fakeIssuer) *auth.Verifier {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	v, err := auth.New(ctx, fi.server.URL, testAudience)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	return v
}

func TestVerifier_Verify_Succeeds(t *testing.T) {
	fi := newFakeIssuer(t)
	v := newVerifier(t, fi)
	token := fi.mint(t, validClaims(fi))

	claims, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-123" || claims.Email != "user@example.com" || claims.Name != "Test User" {
		t.Errorf("got claims %+v, want sub/email/name from the token", claims)
	}
}

func TestVerifier_Verify_RejectsMalformedToken(t *testing.T) {
	fi := newFakeIssuer(t)
	v := newVerifier(t, fi)

	_, err := v.Verify(context.Background(), "not-a-jwt-at-all")
	if !errors.Is(err, user.ErrTokenInvalid) {
		t.Errorf("got err %v, want wrapping ErrTokenInvalid", err)
	}
}

func TestVerifier_Verify_RejectsExpiredToken(t *testing.T) {
	fi := newFakeIssuer(t)
	v := newVerifier(t, fi)

	claims := validClaims(fi)
	claims["iat"] = time.Now().Add(-2 * time.Hour).Unix()
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	token := fi.mint(t, claims)

	_, err := v.Verify(context.Background(), token)
	if !errors.Is(err, user.ErrTokenInvalid) {
		t.Errorf("got err %v, want wrapping ErrTokenInvalid for an expired token", err)
	}
}

func TestVerifier_Verify_RejectsWrongSignature(t *testing.T) {
	fi := newFakeIssuer(t)
	v := newVerifier(t, fi)
	token := fi.mintWithWrongKey(t, validClaims(fi))

	_, err := v.Verify(context.Background(), token)
	if !errors.Is(err, user.ErrTokenInvalid) {
		t.Errorf("got err %v, want wrapping ErrTokenInvalid for a signature the JWKS can't verify", err)
	}
}

// TestVerifier_Verify_RejectsWrongAudience proves audience is the SOLE
// rejection reason: it first mints and verifies the exact same claim set
// unmodified (must succeed), then mints it again with only aud changed
// (must fail) -- so a pass here can't be explained by some other field
// being wrong too, or by audience checking having been silently skipped
// (SkipClientIDCheck) and something else catching it instead.
func TestVerifier_Verify_RejectsWrongAudience(t *testing.T) {
	fi := newFakeIssuer(t)
	v := newVerifier(t, fi)

	baseline := fi.mint(t, validClaims(fi))
	if _, err := v.Verify(context.Background(), baseline); err != nil {
		t.Fatalf("baseline (unmodified valid claims) failed to verify: %v -- if this fails, the audience-rejection test below is meaningless", err)
	}

	wrongAud := validClaims(fi)
	wrongAud["aud"] = "someone-elses-api"
	token := fi.mint(t, wrongAud)

	_, err := v.Verify(context.Background(), token)
	if !errors.Is(err, user.ErrTokenInvalid) {
		t.Errorf("got err %v, want wrapping ErrTokenInvalid for the wrong audience -- audience checking must be enforced, not skipped", err)
	}
}

// TestVerifier_Verify_RejectsWrongIssuer is the fake-JWKS-double
// counterpart to TestAuthIntegration_WrongIssuer_Returns401 (which uses a
// genuinely different issuer path on the real mock server). Here, proving
// issuer is the SOLE rejection reason means minting the identical claim
// set unmodified first (must succeed) before changing only iss (must
// fail) -- same signing key, same everything else.
func TestVerifier_Verify_RejectsWrongIssuer(t *testing.T) {
	fi := newFakeIssuer(t)
	v := newVerifier(t, fi)

	baseline := fi.mint(t, validClaims(fi))
	if _, err := v.Verify(context.Background(), baseline); err != nil {
		t.Fatalf("baseline (unmodified valid claims) failed to verify: %v -- if this fails, the issuer-rejection test below is meaningless", err)
	}

	wrongIssuer := validClaims(fi)
	wrongIssuer["iss"] = "http://a-completely-different-issuer.example/default"
	token := fi.mint(t, wrongIssuer)

	_, err := v.Verify(context.Background(), token)
	if !errors.Is(err, user.ErrTokenInvalid) {
		t.Errorf("got err %v, want wrapping ErrTokenInvalid for an issuer that doesn't match the configured one", err)
	}
}

func TestVerifier_Verify_RejectsMissingClaims(t *testing.T) {
	fi := newFakeIssuer(t)
	v := newVerifier(t, fi)

	claims := validClaims(fi)
	delete(claims, "email")
	delete(claims, "name")
	token := fi.mint(t, claims)

	_, err := v.Verify(context.Background(), token)
	if !errors.Is(err, user.ErrMissingClaims) {
		t.Errorf("got err %v, want ErrMissingClaims -- this token verifies cleanly but the issuer isn't sending what we require, which is a distinct failure mode from a bad/forged token", err)
	}
	if errors.Is(err, user.ErrTokenInvalid) {
		t.Errorf("got err also wrapping ErrTokenInvalid; missing-claims must be distinguishable from token_invalid in the logs")
	}
}

// TestVerifier_Verify_AgainstRealMockIssuer proves discovery + JWKS fetch
// work against mock-oauth2-server's real response shapes, not just our own
// assumptions about the OIDC spec. Requires the compose stack's oidc
// service (make up) -- fails fast with guidance rather than skipping
// silently, matching how the postgres integration tests behave.
func TestVerifier_Verify_AgainstRealMockIssuer(t *testing.T) {
	issuerURL := os.Getenv("OIDC_ISSUER_URL")
	if issuerURL == "" {
		issuerURL = "http://localhost:9090/default"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	v, err := auth.New(ctx, issuerURL, testAudience)
	if err != nil {
		t.Fatalf("auth.New against %q: %v\n\nThe suite needs the mock OIDC issuer. Run `make up` first, or `make test`, which does it for you.", issuerURL, err)
	}

	token := mintFromRealIssuer(t, issuerURL, "demo-admin", "", "")

	claims, err := v.Verify(ctx, token)
	if err != nil {
		t.Fatalf("Verify against a real mock-oauth2-server token: %v", err)
	}
	if claims.Subject != "demo-admin" || claims.Email != "admin@demo.krane" || claims.Name != "Demo Admin" {
		t.Errorf("got claims %+v, want the demo-admin identity baked into docker-compose.yml's JSON_CONFIG", claims)
	}
}

// mintFromRealIssuer POSTs a client_credentials token request to the real
// mock-oauth2-server's token endpoint. clientID selects a JSON_CONFIG
// requestMapping (docker-compose.yml); testSub/testAud are only used when
// clientID is empty, to hit the test_sub/test_aud catchall mapping instead.
func mintFromRealIssuer(t *testing.T, issuerURL, clientID, testSub, testAud string) string {
	t.Helper()

	form := "grant_type=client_credentials&client_secret=unused"
	if clientID != "" {
		form += "&client_id=" + clientID
	} else {
		form += fmt.Sprintf("&client_id=test-client&test_sub=%s&test_aud=%s", testSub, testAud)
	}

	resp, err := http.Post(issuerURL+"/token", "application/x-www-form-urlencoded", strings.NewReader(form))
	if err != nil {
		t.Fatalf("posting to token endpoint: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("closing token response body: %v", err)
		}
	}()

	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding token response: %v", err)
	}
	if body.AccessToken == "" {
		t.Fatalf("token endpoint returned no access_token (http status %d)", resp.StatusCode)
	}
	return body.AccessToken
}
