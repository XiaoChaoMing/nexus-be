package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/logger"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/pkg/redis"
	"github.com/minhnn/nexuslive-be/services/chat/config"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/persistent"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/realtime"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase"
	"github.com/minhnn/nexuslive-be/services/chat/internal/usecase/rooms"
)

const _devGrantTimeout = 15 * time.Second

var (
	errDevOnly   = errors.New("dev-grant runs only with NXL_ENV=dev")
	errUsage     = errors.New("usage: chat dev-grant follow|sub|mod|vip|owner --channel <uuid> --user <uuid> [--tier 1-3] [--minutes N] [--revoke]")
	errBadTier   = errors.New("--tier must be 1, 2 or 3")
	errBadUUIDs  = errors.New("--channel and --user must be uuids")
	errBadAction = errors.New("unknown grant")
)

type grantArgs struct {
	what, channel, user string
	tier, minutes       int
	revoke              bool
}

// DevGrant writes demo data that has no real source yet: follows and subscriptions (Redis copies),
// moderator/VIP roles (channel_roles) and a channel's owner.
func DevGrant(cfg *config.Config, args []string, out io.Writer) error {
	if cfg.App.Env != "dev" {
		return errDevOnly
	}

	a, err := parseGrant(args)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), _devGrantTimeout)
	defer cancel()

	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(1))
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pg.Close()

	rdb, err := redis.New(ctx, cfg.Redis.Addr)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer rdb.Close()

	store := persistent.New(pg)
	rt := realtime.New(rdb, realtime.Options{})
	grants := rooms.New(rooms.Repos{Rooms: store, Messages: store, Users: store, Roles: store, Audience: rt, Presence: rt},
		nil, cfg.Chat.Retention, logger.New("error"))

	if gerr := applyGrant(ctx, grants, a); gerr != nil {
		return gerr
	}

	_, err = fmt.Fprintf(out, "ok: %s revoke=%t channel=%s user=%s\n", a.what, a.revoke, a.channel, a.user)

	return err
}

func parseGrant(args []string) (grantArgs, error) {
	if len(args) == 0 {
		return grantArgs{}, errUsage
	}

	a := grantArgs{what: args[0]}

	fs := flag.NewFlagSet("dev-grant", flag.ContinueOnError)
	fs.StringVar(&a.channel, "channel", "", "channel id")
	fs.StringVar(&a.user, "user", "", "user id")
	fs.IntVar(&a.tier, "tier", 1, "subscription tier (sub)")
	fs.IntVar(&a.minutes, "minutes", 0, "followed this many minutes ago (follow)")
	fs.BoolVar(&a.revoke, "revoke", false, "remove instead of grant")

	if err := fs.Parse(args[1:]); err != nil {
		return grantArgs{}, fmt.Errorf("%w: %w", errUsage, err)
	}

	if _, err := uuid.Parse(a.channel); err != nil {
		return grantArgs{}, errBadUUIDs
	}

	if _, err := uuid.Parse(a.user); err != nil {
		return grantArgs{}, errBadUUIDs
	}

	if a.tier < 1 || a.tier > 3 {
		return grantArgs{}, errBadTier
	}

	return a, nil
}

func applyGrant(ctx context.Context, g usecase.Grants, a grantArgs) error {
	switch a.what {
	case "follow":
		if a.revoke {
			return g.Unfollow(ctx, a.channel, a.user)
		}

		return g.Follow(ctx, a.channel, a.user, time.Now().Add(-time.Duration(a.minutes)*time.Minute))
	case "sub":
		if a.revoke {
			return g.Unsubscribe(ctx, a.channel, a.user)
		}

		return g.Subscribe(ctx, a.channel, a.user, a.tier)
	case "mod", "vip":
		if a.revoke {
			return g.RevokeRole(ctx, a.channel, a.user)
		}

		role := entity.ChannelRoleModerator
		if a.what == "vip" {
			role = entity.ChannelRoleVIP
		}

		return g.GrantRole(ctx, a.channel, a.user, role)
	case "owner":
		return g.SetOwner(ctx, a.channel, a.user)
	}

	return fmt.Errorf("%w %q: %w", errBadAction, a.what, errUsage)
}
