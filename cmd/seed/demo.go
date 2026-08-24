package main

// The three demo identities the Makefile's `token` target and this seed
// generator both need to agree on. They must match docker-compose.yml's
// `oidc` service JSON_CONFIG exactly (requestMappings on client_id
// demo-admin/demo-contributor/demo-attendee) -- that's YAML/JSON, so it
// can't share this Go const block directly. If the two drift, cmd/seed's
// own token-mint self-check (main.go) is what catches it: it mints a real
// token from the mock issuer and reads back the sub claim the issuer
// actually put in it, rather than trusting these constants describe reality.
//
// clientID is the OAuth client_id demo-<role> requests present to the mock
// issuer (see docker-compose.yml and `make token USER=...`); subject is the
// resulting sub claim. In this config they're identical strings, but kept
// as separate constants so a future divergence doesn't require touching
// call sites.
type demoIdentity struct {
	clientID string
	subject  string
	email    string
	name     string
	role     string
}

var demoIdentities = []demoIdentity{
	{clientID: "demo-admin", subject: "demo-admin", email: "admin@demo.krane", name: "Demo Admin", role: "admin"},
	{clientID: "demo-contributor", subject: "demo-contributor", email: "contributor@demo.krane", name: "Demo Contributor", role: "contributor"},
	{clientID: "demo-attendee", subject: "demo-attendee", email: "attendee@demo.krane", name: "Demo Attendee", role: "attendee"},
}

// seedDemoEventName is the fixed, deterministic name of the event the three
// demo identities are wired into as event_members -- so `make token
// USER=admin|contributor|attendee` exercises real, role-shaped data end to
// end (item 10's presenters) instead of landing on an empty roster.
const seedDemoEventName = "Seed Demo Event"
