package main

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// Fixed, deterministic identities for the events findable by name -- see
// the plan's decision 3. Session titles double as lookup keys for the two
// DST fixture sessions specifically (generate_test.go, and cmd/seed's own
// self-check in main.go).
const (
	seedDSTSpringForwardEventName    = "Seed DST Spring-Forward Fixture"
	seedDSTFallBackEventName         = "Seed DST Fall-Back Fixture"
	seedLargeEventName               = "Seed Large-Scale Fixture Event"
	seedDSTSpringForwardSessionTitle = "Crosses 2026-03-08 Spring-Forward Gap"
	seedDSTFallBackSessionTitle      = "Crosses 2026-11-01 Fall-Back Overlap"
)

// DefaultSeed is the fixed math/rand seed main.go's real run uses, and the
// one generate_test.go exercises -- see decision 6 of the feature plan:
// dataset *content* is deterministic given a fixed seed, even though row
// ids (UUIDv7) never are.
const DefaultSeed int64 = 20260214

// seedBaseTime is every generated row's created_at/updated_at, offset isn't
// needed since real chronology doesn't matter for seed data -- using a fixed
// instant (not time.Now()) keeps generated *content* fully deterministic
// given a fixed seed, which TestGenerateDataset_SameSeedProducesIdenticalContent
// relies on. Row ids are still fresh UUIDv7s every run (see uuidv7.go) --
// nothing depends on those being stable across runs.
var seedBaseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

var bulkTimezones = []string{"Asia/Colombo", "UTC", "Europe/London", "America/Los_Angeles", "Australia/Sydney"}

// Config sizes a generated dataset. DefaultConfig is FEATURES.md item 14's
// real target (50 events, 5,000 users, 50,000 invitations); tests use it
// directly too since generation is pure Go with no I/O and runs in well
// under a second even at full scale.
type Config struct {
	UserCount              int // includes the 3 demo identities
	BulkEventCount         int // "ordinary" events; +4 special-cased ones = the real event count
	InvitationsPerEvent    int
	MinRoomsPerEvent       int
	MaxRoomsPerEvent       int
	MinSessionsPerEvent    int
	MaxSessionsPerEvent    int
	LargeEventRoomCount    int
	LargeEventSessionCount int
}

func DefaultConfig() Config {
	return Config{
		UserCount:              5000,
		BulkEventCount:         46,
		InvitationsPerEvent:    1000,
		MinRoomsPerEvent:       2,
		MaxRoomsPerEvent:       6,
		MinSessionsPerEvent:    5,
		MaxSessionsPerEvent:    20,
		LargeEventRoomCount:    10,
		LargeEventSessionCount: 500,
	}
}

// Dataset is a complete, internally-consistent set of rows ready to load --
// every FK reference already resolved to a real (client-generated) UUIDv7,
// built from the same domain structs the repositories use.
type Dataset struct {
	Users       []user.User
	Events      []event.Event
	Members     []member.Member
	Rooms       []room.Room
	Sessions    []session.Session
	Invitations []invitation.Invitation
}

// GenerateDataset is pure and DB-free: everything about *what* gets
// generated is decided here, deterministically from seed; cmd/seed/load.go
// is the only part of this package that talks to Postgres.
func GenerateDataset(cfg Config, seed int64) Dataset {
	r := rand.New(rand.NewSource(seed))
	users := generateUsers(cfg)

	externalPool := make([]string, len(users))
	for i, u := range users {
		externalPool[i] = u.ID
	}
	r.Shuffle(len(externalPool), func(i, j int) { externalPool[i], externalPool[j] = externalPool[j], externalPool[i] })

	demoUserID := make(map[string]string, len(demoIdentities))
	for i := range demoIdentities {
		demoUserID[demoIdentities[i].subject] = users[i].ID
	}

	g := &generator{cfg: cfg, r: r, users: users, externalPool: externalPool, demoUserID: demoUserID, sched: newSpeakerSchedule()}

	for i := 0; i < cfg.BulkEventCount; i++ {
		g.addBulkEvent(i)
	}
	g.addDSTFixtureEvent(
		seedDSTSpringForwardEventName, seedDSTSpringForwardSessionTitle,
		"2026-03-08T01:30:00", "2026-03-08T03:30:00",
		mustLoadLocation("America/New_York"), 2026, time.March, 1, 15,
	)
	g.addDSTFixtureEvent(
		seedDSTFallBackEventName, seedDSTFallBackSessionTitle,
		"2026-11-01T00:30:00", "2026-11-01T02:30:00",
		mustLoadLocation("America/New_York"), 2026, time.October, 25, 8+31, // Oct 25 -> Nov 8
	)
	g.addLargeEvent()
	g.addDemoEvent()

	return Dataset{
		Users:       users,
		Events:      g.events,
		Members:     g.members,
		Rooms:       g.rooms,
		Sessions:    g.sessions,
		Invitations: g.invitations,
	}
}

func generateUsers(cfg Config) []user.User {
	users := make([]user.User, 0, cfg.UserCount)
	for _, d := range demoIdentities {
		users = append(users, user.User{
			ID: newUUIDv7(), Subject: d.subject, Email: d.email, Name: d.name,
			CreatedAt: seedBaseTime, UpdatedAt: seedBaseTime,
		})
	}
	for n := 1; len(users) < cfg.UserCount; n++ {
		users = append(users, user.User{
			ID:        newUUIDv7(),
			Subject:   fmt.Sprintf("seed-user-%05d", n),
			Email:     fmt.Sprintf("user%05d@seed.example", n),
			Name:      fmt.Sprintf("Seed User %05d", n),
			CreatedAt: seedBaseTime,
			UpdatedAt: seedBaseTime,
		})
	}
	return users
}

// generator accumulates rows across every addXEvent call, sharing one
// speaker schedule (decision 3: the double-booking guard is table-wide, not
// per event) and one shuffled pool of every user id (the "external speaker"
// fallback / bias -- see pickCandidatesFor).
type generator struct {
	cfg          Config
	r            *rand.Rand
	users        []user.User
	externalPool []string
	demoUserID   map[string]string
	sched        *speakerSchedule
	eventSeq     int

	events      []event.Event
	rooms       []room.Room
	sessions    []session.Session
	members     []member.Member
	invitations []invitation.Invitation
}

func (g *generator) addBulkEvent(i int) {
	tz := bulkTimezones[g.r.Intn(len(bulkTimezones))]
	loc := mustLoadLocation(tz)
	name := fmt.Sprintf("Seed Bulk Event %03d", i+1)
	startsAt := time.Date(2026, 1, 1, 0, 0, 0, 0, loc).AddDate(0, 0, i*3)
	endsAt := startsAt.AddDate(0, 0, 14)

	ev := g.newEvent(name, tz, startsAt, endsAt)
	memberIDs := g.addStandardMembers(ev.ID, nil)
	roomIDs := g.addRooms(ev.ID, g.cfg.MinRoomsPerEvent+g.r.Intn(g.cfg.MaxRoomsPerEvent-g.cfg.MinRoomsPerEvent+1))
	sessionCount := g.cfg.MinSessionsPerEvent + g.r.Intn(g.cfg.MaxSessionsPerEvent-g.cfg.MinSessionsPerEvent+1)
	g.distributeSessions(ev, roomIDs, memberIDs, sessionCount, name)
	g.addInvitations(ev)
}

func (g *generator) addLargeEvent() {
	loc := mustLoadLocation("Asia/Colombo")
	startsAt := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	endsAt := startsAt.AddDate(0, 0, 90)

	ev := g.newEvent(seedLargeEventName, "Asia/Colombo", startsAt, endsAt)
	memberIDs := g.addStandardMembers(ev.ID, nil)
	roomIDs := g.addRooms(ev.ID, g.cfg.LargeEventRoomCount)
	g.distributeSessions(ev, roomIDs, memberIDs, g.cfg.LargeEventSessionCount, seedLargeEventName)
	g.addInvitations(ev)
}

func (g *generator) addDemoEvent() {
	loc := mustLoadLocation("Asia/Colombo")
	startsAt := time.Date(2026, 9, 1, 0, 0, 0, 0, loc)
	endsAt := startsAt.AddDate(0, 0, 14)

	ev := g.newEvent(seedDemoEventName, "Asia/Colombo", startsAt, endsAt)
	memberIDs := g.addStandardMembers(ev.ID, demoIdentities)
	roomIDs := g.addRooms(ev.ID, g.cfg.MinRoomsPerEvent+g.r.Intn(g.cfg.MaxRoomsPerEvent-g.cfg.MinRoomsPerEvent+1))
	sessionCount := g.cfg.MinSessionsPerEvent + g.r.Intn(g.cfg.MaxSessionsPerEvent-g.cfg.MinSessionsPerEvent+1)
	g.distributeSessions(ev, roomIDs, memberIDs, sessionCount, seedDemoEventName)
	g.addInvitations(ev)
}

// addDSTFixtureEvent builds a 2-room event: Room A hosts exactly one
// session -- the DST-crossing fixture, resolved via the same
// utils.ResolveLocalTime the real session write path uses, so it's only
// correct because a tested function produced it -- and Room B hosts a
// couple of ordinary filler sessions so the event isn't a single-row
// curiosity.
func (g *generator) addDSTFixtureEvent(name, fixtureTitle, localStart, localEnd string, loc *time.Location, year int, month time.Month, day, windowDays int) {
	startsAt := time.Date(year, month, day, 0, 0, 0, 0, loc)
	endsAt := startsAt.AddDate(0, 0, windowDays)

	ev := g.newEvent(name, "America/New_York", startsAt, endsAt)
	memberIDs := g.addStandardMembers(ev.ID, nil)
	roomIDs := g.addRooms(ev.ID, 2)

	start, err := utils.ResolveLocalTime(localStart, loc)
	if err != nil {
		panic(fmt.Sprintf("addDSTFixtureEvent(%q): resolving fixture start %q: %v", name, localStart, err))
	}
	end, err := utils.ResolveLocalTime(localEnd, loc)
	if err != nil {
		panic(fmt.Sprintf("addDSTFixtureEvent(%q): resolving fixture end %q: %v", name, localEnd, err))
	}
	candidates := g.pickCandidatesFor(memberIDs)
	speaker := pickSpeaker(candidates, g.sched, start, end)
	g.sched.reserve(speaker, start, end)
	g.sessions = append(g.sessions, session.Session{
		ID: newUUIDv7(), EventID: ev.ID, RoomID: roomIDs[0], SpeakerID: speaker,
		Title: fixtureTitle, StartsAt: start, EndsAt: end, Version: 1,
		CreatedAt: seedBaseTime, UpdatedAt: seedBaseTime,
	})

	g.addGenericSessions(ev, roomIDs[1], memberIDs, startsAt.Add(9*time.Hour), 2, name+" Filler")
	g.addInvitations(ev)
}

func (g *generator) newEvent(name, timezone string, startsAt, endsAt time.Time) event.Event {
	ev := event.Event{
		ID: newUUIDv7(), Name: name, Timezone: timezone,
		StartsAt: startsAt, EndsAt: endsAt, Version: 1,
		CreatedAt: seedBaseTime, UpdatedAt: seedBaseTime,
	}
	g.events = append(g.events, ev)
	g.eventSeq++
	return ev
}

func (g *generator) addRooms(eventID string, count int) []string {
	ids := make([]string, count)
	for k := 0; k < count; k++ {
		r := room.Room{ID: newUUIDv7(), EventID: eventID, Name: roomName(k), Version: 1, CreatedAt: seedBaseTime, UpdatedAt: seedBaseTime}
		g.rooms = append(g.rooms, r)
		ids[k] = r.ID
	}
	return ids
}

func roomName(k int) string {
	if k < 26 {
		return "Room " + string(rune('A'+k))
	}
	return fmt.Sprintf("Room %d", k+1)
}

// addStandardMembers seeds a realistic roster (1-2 admins, 2-5 contributors,
// 10-40 attendees) drawn from the full user pool. demo, when non-nil, wires
// the 3 fixed demo identities in at their fixed roles first (decision 4) and
// excludes them from the random draw so they're never double-picked.
func (g *generator) addStandardMembers(eventID string, demo []demoIdentity) []string {
	exclude := make(map[string]bool, len(demo))
	var memberIDs []string

	for _, d := range demo {
		id := g.demoUserID[d.subject]
		exclude[id] = true
		g.members = append(g.members, member.Member{
			ID: newUUIDv7(), EventID: eventID, UserID: id, Role: d.role, Version: 1,
			CreatedAt: seedBaseTime, UpdatedAt: seedBaseTime,
		})
		memberIDs = append(memberIDs, id)
	}

	addRole := func(count int, role string) {
		picked := g.pickDistinctUsers(count, exclude)
		for _, id := range picked {
			exclude[id] = true
			g.members = append(g.members, member.Member{
				ID: newUUIDv7(), EventID: eventID, UserID: id, Role: role, Version: 1,
				CreatedAt: seedBaseTime, UpdatedAt: seedBaseTime,
			})
			memberIDs = append(memberIDs, id)
		}
	}
	addRole(1+g.r.Intn(2), "admin")
	addRole(2+g.r.Intn(4), "contributor")
	addRole(10+g.r.Intn(31), "attendee")

	return memberIDs
}

// pickDistinctUsers returns up to n distinct user ids not in exclude, via
// rejection sampling -- fine at production scale (a few dozen requested out
// of 5,000 users), but rejection sampling degrades badly, and can loop
// forever, as the request approaches the available pool size. Capping n at
// what's actually available makes this safe for any Config, not just
// DefaultConfig's numbers -- caught by
// TestTruncateAndLoad_RunTwiceIsIdempotent hanging against smallConfig's
// 40-user pool (admin+contributor+attendee can ask for up to 47).
func (g *generator) pickDistinctUsers(n int, exclude map[string]bool) []string {
	if available := len(g.users) - len(exclude); n > available {
		n = available
	}
	if n <= 0 {
		return nil
	}
	picked := make([]string, 0, n)
	seen := make(map[string]bool, n)
	for len(picked) < n {
		id := g.users[g.r.Intn(len(g.users))].ID
		if seen[id] || exclude[id] {
			continue
		}
		seen[id] = true
		picked = append(picked, id)
	}
	return picked
}

// pickCandidatesFor builds a speaker candidate order for one session. 40% of
// the time it goes straight to the full user pool (a non-member speaker,
// docs/requirements.md D3), otherwise it tries this event's own members
// first and falls back to the full pool -- so a member speaker is common but
// never guaranteed, matching the mix decision 3 asks for.
func (g *generator) pickCandidatesFor(memberIDs []string) []string {
	if len(memberIDs) == 0 || g.r.Intn(100) < 40 {
		return g.externalPool
	}
	shuffled := append([]string(nil), memberIDs...)
	g.r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	return append(shuffled, g.externalPool...)
}

func (g *generator) distributeSessions(ev event.Event, roomIDs, memberIDs []string, totalCount int, titlePrefix string) {
	n := len(roomIDs)
	base, rem := totalCount/n, totalCount%n
	for k, roomID := range roomIDs {
		count := base
		if k < rem {
			count++
		}
		g.addGenericSessions(ev, roomID, memberIDs, ev.StartsAt.Add(9*time.Hour), count, titlePrefix)
	}
}

// addGenericSessions places count sessions in one room, back to back with a
// 30-minute gap starting at cursor -- sequential placement makes room
// non-overlap trivially true by construction. Speaker overlap is checked
// against the table-wide speakerSchedule shared by the whole generator run.
func (g *generator) addGenericSessions(ev event.Event, roomID string, memberIDs []string, cursor time.Time, count int, titlePrefix string) {
	for j := 0; j < count; j++ {
		duration := time.Duration(30+30*g.r.Intn(3)) * time.Minute
		start := cursor
		end := start.Add(duration)
		cursor = end.Add(30 * time.Minute)

		candidates := g.pickCandidatesFor(memberIDs)
		speaker := pickSpeaker(candidates, g.sched, start, end)
		g.sched.reserve(speaker, start, end)

		g.sessions = append(g.sessions, session.Session{
			ID: newUUIDv7(), EventID: ev.ID, RoomID: roomID, SpeakerID: speaker,
			Title:    fmt.Sprintf("%s Session %d", titlePrefix, j+1),
			StartsAt: start, EndsAt: end, Version: 1,
			CreatedAt: seedBaseTime, UpdatedAt: seedBaseTime,
		})
	}
}

// addInvitations gives ev exactly cfg.InvitationsPerEvent invitations: ~80%
// target real seeded users (user_id set), ~20% fabricated addresses with no
// account (user_id nil, docs/requirements.md D2) built from a per-event
// sequential counter so uniqueness within UNIQUE(event_id, email) holds by
// construction rather than RNG luck.
func (g *generator) addInvitations(ev event.Event) {
	realCount := int(float64(g.cfg.InvitationsPerEvent) * 0.8)
	externalCount := g.cfg.InvitationsPerEvent - realCount

	pickRole := func() string {
		switch x := g.r.Intn(100); {
		case x < 80:
			return "attendee"
		case x < 95:
			return "contributor"
		default:
			return "admin"
		}
	}

	for _, idx := range g.r.Perm(len(g.users))[:realCount] {
		u := g.users[idx]
		uid := u.ID
		g.invitations = append(g.invitations, invitation.Invitation{
			ID: newUUIDv7(), EventID: ev.ID, UserID: &uid, Email: u.Email, Role: pickRole(),
			CreatedAt: seedBaseTime, UpdatedAt: seedBaseTime,
		})
	}
	for n := 1; n <= externalCount; n++ {
		g.invitations = append(g.invitations, invitation.Invitation{
			ID: newUUIDv7(), EventID: ev.ID, UserID: nil,
			Email: fmt.Sprintf("seed-ext-%d-%d@seed-external.example", g.eventSeq, n),
			Role:  pickRole(), CreatedAt: seedBaseTime, UpdatedAt: seedBaseTime,
		})
	}
}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("mustLoadLocation: " + name + ": " + err.Error())
	}
	return loc
}

// speakerSchedule tracks every speaker's booked intervals across the WHOLE
// dataset -- not per event -- because the sessions table's future
// (speaker_id, tstzrange) EXCLUDE constraint (item 16) is scoped to the
// entire table, not partitioned by event.
type speakerSchedule struct {
	busy map[string][]timeRange
}

type timeRange struct{ start, end time.Time }

func newSpeakerSchedule() *speakerSchedule {
	return &speakerSchedule{busy: make(map[string][]timeRange)}
}

func (s *speakerSchedule) overlaps(userID string, start, end time.Time) bool {
	for _, tr := range s.busy[userID] {
		if start.Before(tr.end) && tr.start.Before(end) {
			return true
		}
	}
	return false
}

func (s *speakerSchedule) reserve(userID string, start, end time.Time) {
	s.busy[userID] = append(s.busy[userID], timeRange{start, end})
}

// pickSpeaker returns the first candidate free for [start, end). candidates
// is always ordered from a scarce, likely-free pool (an event's own
// members) through to the full user pool, so this practically never
// exhausts its list -- but it panics rather than silently double-booking a
// speaker if it ever does, since that would be exactly the invariant
// violation item 16 exists to prevent.
func pickSpeaker(candidates []string, sched *speakerSchedule, start, end time.Time) string {
	for _, c := range candidates {
		if !sched.overlaps(c, start, end) {
			return c
		}
	}
	panic("pickSpeaker: no candidate free for this slot -- grow the candidate pool or reduce session density")
}

// noSpeakerOrRoomOverlap is the table-wide guard against building data that
// violates item 16's future EXCLUDE constraints before they exist. It's
// deliberately a standalone, independently-testable function rather than
// trusting generation to never trip it -- see
// TestNoSpeakerOrRoomOverlap_DetectsForcedSpeakerOverlap/RoomOverlap in
// generate_test.go.
func noSpeakerOrRoomOverlap(sessions []session.Session) error {
	if err := noOverlapByKey(sessions, func(s session.Session) string { return s.RoomID }, "room"); err != nil {
		return err
	}
	return noOverlapByKey(sessions, func(s session.Session) string { return s.SpeakerID }, "speaker")
}

func noOverlapByKey(sessions []session.Session, key func(session.Session) string, label string) error {
	byKey := make(map[string][]session.Session)
	for _, s := range sessions {
		k := key(s)
		byKey[k] = append(byKey[k], s)
	}
	for k, group := range byKey {
		sort.Slice(group, func(i, j int) bool { return group[i].StartsAt.Before(group[j].StartsAt) })
		for i := 1; i < len(group); i++ {
			prev, cur := group[i-1], group[i]
			// Half-open [) convention (migrations/20260823164421_init_schema.up.sql):
			// touching endpoints are not an overlap, so this must match &&'s semantics.
			if cur.StartsAt.Before(prev.EndsAt) {
				return fmt.Errorf("%s %q has overlapping sessions %q (%s-%s) and %q (%s-%s)",
					label, k, prev.Title, prev.StartsAt, prev.EndsAt, cur.Title, cur.StartsAt, cur.EndsAt)
			}
		}
	}
	return nil
}
