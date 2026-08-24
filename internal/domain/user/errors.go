package user

import "errors"

// ErrTokenInvalid wraps any failure in the token itself: bad signature,
// expired, wrong audience, or wrong issuer. The "someone sent garbage"
// bucket -- adapter/auth returns it, http/middleware logs and rejects on
// it. Lives here, not in adapter/auth, so http can distinguish it from
// ErrMissingClaims for logging without importing adapter directly.
var ErrTokenInvalid = errors.New("auth: token invalid")

// ErrMissingClaims means a token verified cleanly -- real signature, live,
// right audience and issuer -- but lacks sub/email/name. Distinct from
// ErrTokenInvalid: it signals the issuer isn't sending what this API
// requires, not that the caller is malicious.
var ErrMissingClaims = errors.New("auth: token missing required claims")
