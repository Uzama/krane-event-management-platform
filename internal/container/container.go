// Package container is the composition root: it builds concrete adapters and
// injects them into domain services and into http. It may import everything;
// nothing may import it.
package container

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Uzama/krane-event-management-platform/internal/adapter/auth"
	"github.com/Uzama/krane-event-management-platform/internal/adapter/authz"
	"github.com/Uzama/krane-event-management-platform/internal/adapter/postgres"
	"github.com/Uzama/krane-event-management-platform/internal/domain/event"
	"github.com/Uzama/krane-event-management-platform/internal/domain/invitation"
	"github.com/Uzama/krane-event-management-platform/internal/domain/member"
	"github.com/Uzama/krane-event-management-platform/internal/domain/room"
	"github.com/Uzama/krane-event-management-platform/internal/domain/session"
	"github.com/Uzama/krane-event-management-platform/internal/domain/user"
	"github.com/Uzama/krane-event-management-platform/internal/utils"
)

// oidcDiscoveryTimeout bounds OIDC discovery the same way utils.NewPool
// bounds its own ping -- New's caller (bootstrap.Boot) hands in a
// long-lived, no-deadline context, so New applies its own timeout rather
// than relying on the caller for fail-fast behaviour.
const oidcDiscoveryTimeout = 5 * time.Second

// Container holds every shared dependency the rest of the app is wired
// against.
type Container struct {
	Config       utils.Config
	Logger       *slog.Logger
	DB           *pgxpool.Pool
	AuthVerifier *auth.Verifier
	Users        *user.Service
	Authz        *authz.Policy
	Events       *event.Service
	Members      *member.Service
	Rooms        *room.Service
	Sessions     *session.Service
	Invitations  *invitation.Service
}

// New builds every dependency and proves the database and the OIDC issuer
// are both reachable before returning -- a bad DATABASE_URL or a
// misconfigured OIDCIssuerURL fails boot here, not on the first request.
func New(ctx context.Context, cfg utils.Config) (*Container, error) {
	logger := utils.NewLogger(cfg)

	pool, err := utils.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, oidcDiscoveryTimeout)
	defer cancel()

	verifier, err := auth.New(discoveryCtx, cfg.OIDCIssuerURL, cfg.OIDCAudience)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("wiring auth verifier: %w", err)
	}

	users := user.NewService(postgres.NewUserRepository(pool))

	policy, err := authz.New(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("wiring authz policy: %w", err)
	}

	events := event.NewService(postgres.NewEventRepository(pool))
	members := member.NewService(postgres.NewMemberRepository(pool))
	rooms := room.NewService(postgres.NewRoomRepository(pool))
	sessions := session.NewService(postgres.NewSessionRepository(pool))
	invitations := invitation.NewService(postgres.NewInvitationRepository(pool))

	return &Container{
		Config:       cfg,
		Logger:       logger,
		DB:           pool,
		AuthVerifier: verifier,
		Users:        users,
		Authz:        policy,
		Events:       events,
		Members:      members,
		Rooms:        rooms,
		Sessions:     sessions,
		Invitations:  invitations,
	}, nil
}

// Close releases every resource the container holds.
func (c *Container) Close() {
	c.DB.Close()
}
