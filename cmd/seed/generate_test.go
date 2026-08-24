package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
)

func TestGenerateDataset_ExactCounts(t *testing.T) {
	cfg := DefaultConfig()
	ds := GenerateDataset(cfg, DefaultSeed)

	wantEvents := cfg.BulkEventCount + 4 // 2 DST fixtures + 1 large fixture + 1 demo event
	if len(ds.Events) != wantEvents {
		t.Errorf("got %d events, want %d", len(ds.Events), wantEvents)
	}
	if len(ds.Users) != cfg.UserCount {
		t.Errorf("got %d users, want %d", len(ds.Users), cfg.UserCount)
	}
	wantInvitations := wantEvents * cfg.InvitationsPerEvent
	if len(ds.Invitations) != wantInvitations {
		t.Errorf("got %d invitations, want %d", len(ds.Invitations), wantInvitations)
	}
}

func TestGenerateDataset_NoSpeakerOrRoomOverlap(t *testing.T) {
	ds := GenerateDataset(DefaultConfig(), DefaultSeed)

	if err := noSpeakerOrRoomOverlap(ds.Sessions); err != nil {
		t.Fatalf("generated dataset violates item 16's future EXCLUDE scope: %v", err)
	}
}

// TestNoSpeakerOrRoomOverlap_DetectsForcedSpeakerOverlap proves the checker
// is load-bearing -- it must actually fail on a real overlap, not just
// happen to pass because generation never produces one. Same discipline
// FAILURES.md requires of the item-09 admin-lock race test: force the
// violation, watch the guard catch it.
func TestNoSpeakerOrRoomOverlap_DetectsForcedSpeakerOverlap(t *testing.T) {
	ds := GenerateDataset(DefaultConfig(), DefaultSeed)
	if err := noSpeakerOrRoomOverlap(ds.Sessions); err != nil {
		t.Fatalf("fixture dataset must be valid before it's mutated: %v", err)
	}

	sessions := append([]session.Session(nil), ds.Sessions...)
	a, b := findTwoSessionsWithDistinctSpeakers(t, sessions)
	sessions[b].SpeakerID = sessions[a].SpeakerID
	sessions[b].StartsAt = sessions[a].StartsAt
	sessions[b].EndsAt = sessions[a].EndsAt

	if err := noSpeakerOrRoomOverlap(sessions); err == nil {
		t.Fatal("forced two sessions onto the same speaker at the same instant, but noSpeakerOrRoomOverlap reported no error")
	}
}

func TestNoSpeakerOrRoomOverlap_DetectsForcedRoomOverlap(t *testing.T) {
	ds := GenerateDataset(DefaultConfig(), DefaultSeed)
	if err := noSpeakerOrRoomOverlap(ds.Sessions); err != nil {
		t.Fatalf("fixture dataset must be valid before it's mutated: %v", err)
	}

	sessions := append([]session.Session(nil), ds.Sessions...)
	a, b := findTwoSessionsInSameRoom(t, sessions)
	sessions[b].StartsAt = sessions[a].StartsAt
	sessions[b].EndsAt = sessions[a].EndsAt
	// Give them different speakers so only the room dimension is forced to overlap.
	sessions[b].SpeakerID = sessions[a].SpeakerID + "-different"

	if err := noSpeakerOrRoomOverlap(sessions); err == nil {
		t.Fatal("forced two sessions into the same room at the same instant, but noSpeakerOrRoomOverlap reported no error")
	}
}

func findTwoSessionsWithDistinctSpeakers(t *testing.T, sessions []session.Session) (int, int) {
	t.Helper()
	for i := range sessions {
		for j := range sessions {
			if i != j && sessions[i].SpeakerID != sessions[j].SpeakerID {
				return i, j
			}
		}
	}
	t.Fatal("no two sessions with distinct speakers found")
	return 0, 0
}

func findTwoSessionsInSameRoom(t *testing.T, sessions []session.Session) (int, int) {
	t.Helper()
	byRoom := map[string][]int{}
	for i, s := range sessions {
		byRoom[s.RoomID] = append(byRoom[s.RoomID], i)
	}
	for _, idxs := range byRoom {
		if len(idxs) >= 2 {
			return idxs[0], idxs[1]
		}
	}
	t.Fatal("no room with two or more sessions found")
	return 0, 0
}

// TestGenerateDataset_DSTSessions_CorrectDurations reuses item 12's own
// proven-correct dates and local times (internal/http/sessions_integration_test.go)
// so the seed's DST fixture is right for the same reason the real write path
// is right: utils.ResolveLocalTime produced both.
func TestGenerateDataset_DSTSessions_CorrectDurations(t *testing.T) {
	ds := GenerateDataset(DefaultConfig(), DefaultSeed)

	spring := findSessionByTitle(t, ds, seedDSTSpringForwardEventName, seedDSTSpringForwardSessionTitle)
	if got := spring.EndsAt.Sub(spring.StartsAt); got != 60*time.Minute {
		t.Errorf("spring-forward session: got duration %v, want 60m (the 2-3am hour never happened)", got)
	}
	if _, offset := spring.StartsAt.Zone(); offset != -5*3600 {
		t.Errorf("spring-forward starts_at offset = %ds, want -05:00 (EST, before the transition)", offset)
	}
	if _, offset := spring.EndsAt.Zone(); offset != -4*3600 {
		t.Errorf("spring-forward ends_at offset = %ds, want -04:00 (EDT, after the transition)", offset)
	}

	fall := findSessionByTitle(t, ds, seedDSTFallBackEventName, seedDSTFallBackSessionTitle)
	if got := fall.EndsAt.Sub(fall.StartsAt); got != 180*time.Minute {
		t.Errorf("fall-back session: got duration %v, want 180m (the 1-2am hour happened twice)", got)
	}
}

func findSessionByTitle(t *testing.T, ds Dataset, eventName, title string) session.Session {
	t.Helper()
	var eventID string
	for _, e := range ds.Events {
		if e.Name == eventName {
			eventID = e.ID
		}
	}
	if eventID == "" {
		t.Fatalf("no generated event named %q", eventName)
	}
	for _, s := range ds.Sessions {
		if s.EventID == eventID && s.Title == title {
			return s
		}
	}
	t.Fatalf("no session titled %q in event %q", title, eventName)
	return session.Session{}
}

func TestGenerateDataset_DemoIdentitiesWiredToDemoEvent(t *testing.T) {
	ds := GenerateDataset(DefaultConfig(), DefaultSeed)

	var demoEventID string
	for _, e := range ds.Events {
		if e.Name == seedDemoEventName {
			demoEventID = e.ID
		}
	}
	if demoEventID == "" {
		t.Fatalf("no generated event named %q", seedDemoEventName)
	}

	for _, want := range demoIdentities {
		var userID string
		for _, u := range ds.Users {
			if u.Subject == want.subject {
				if u.Email != want.email || u.Name != want.name {
					t.Errorf("user %q: got email/name %q/%q, want %q/%q", want.subject, u.Email, u.Name, want.email, want.name)
				}
				userID = u.ID
			}
		}
		if userID == "" {
			t.Fatalf("no generated user with subject %q", want.subject)
		}

		found := false
		for _, m := range ds.Members {
			if m.EventID == demoEventID && m.UserID == userID {
				found = true
				if m.Role != want.role {
					t.Errorf("member %q on %q: got role %q, want %q", want.subject, seedDemoEventName, m.Role, want.role)
				}
			}
		}
		if !found {
			t.Errorf("subject %q has no event_members row on %q", want.subject, seedDemoEventName)
		}
	}
}

func TestGenerateDataset_InvitationsPerEvent_ExactCountAndUniqueEmails(t *testing.T) {
	cfg := DefaultConfig()
	ds := GenerateDataset(cfg, DefaultSeed)

	byEvent := map[string][]invitation.Invitation{}
	for _, inv := range ds.Invitations {
		byEvent[inv.EventID] = append(byEvent[inv.EventID], inv)
	}
	if len(byEvent) != len(ds.Events) {
		t.Fatalf("got invitations grouped into %d events, want %d", len(byEvent), len(ds.Events))
	}

	for _, e := range ds.Events {
		invs := byEvent[e.ID]
		if len(invs) != cfg.InvitationsPerEvent {
			t.Errorf("event %q: got %d invitations, want %d", e.Name, len(invs), cfg.InvitationsPerEvent)
		}
		seen := make(map[string]struct{}, len(invs))
		externalCount := 0
		for _, inv := range invs {
			if _, dup := seen[inv.Email]; dup {
				t.Errorf("event %q: duplicate invitation email %q (violates UNIQUE(event_id, email))", e.Name, inv.Email)
			}
			seen[inv.Email] = struct{}{}
			if strings.HasSuffix(inv.Email, "@seed-external.example") {
				externalCount++
				if inv.UserID != nil {
					t.Errorf("event %q: external invitation %q has a non-nil UserID", e.Name, inv.Email)
				}
			} else if inv.UserID == nil {
				t.Errorf("event %q: invitation %q targets a real seeded email but has a nil UserID", e.Name, inv.Email)
			}
		}
		if externalCount == 0 {
			t.Errorf("event %q: no external (no-account) invitations generated -- D2's nullable-user_id case isn't exercised", e.Name)
		}
	}
}

func TestGenerateDataset_SameSeedProducesIdenticalContent(t *testing.T) {
	cfg := DefaultConfig()
	a := GenerateDataset(cfg, DefaultSeed)
	b := GenerateDataset(cfg, DefaultSeed)

	if len(a.Users) != len(b.Users) {
		t.Fatalf("user count differs across runs: %d vs %d", len(a.Users), len(b.Users))
	}
	for i := range a.Users {
		if a.Users[i].Subject != b.Users[i].Subject || a.Users[i].Email != b.Users[i].Email || a.Users[i].Name != b.Users[i].Name {
			t.Fatalf("user %d content differs across runs: %+v vs %+v", i, a.Users[i], b.Users[i])
		}
	}

	if len(a.Events) != len(b.Events) {
		t.Fatalf("event count differs across runs: %d vs %d", len(a.Events), len(b.Events))
	}
	for i := range a.Events {
		if a.Events[i].Name != b.Events[i].Name || a.Events[i].Timezone != b.Events[i].Timezone || !a.Events[i].StartsAt.Equal(b.Events[i].StartsAt) {
			t.Fatalf("event %d content differs across runs: %+v vs %+v", i, a.Events[i], b.Events[i])
		}
	}

	if len(a.Invitations) != len(b.Invitations) {
		t.Fatalf("invitation count differs across runs: %d vs %d", len(a.Invitations), len(b.Invitations))
	}
	for i := range a.Invitations {
		if a.Invitations[i].Email != b.Invitations[i].Email || a.Invitations[i].Role != b.Invitations[i].Role {
			t.Fatalf("invitation %d content differs across runs: %+v vs %+v", i, a.Invitations[i], b.Invitations[i])
		}
	}
}
