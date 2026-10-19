package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/minhnn/nexuslive-be/pkg/postgres"
	"github.com/minhnn/nexuslive-be/services/chat/config"
	"github.com/minhnn/nexuslive-be/services/chat/internal/entity"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo"
	"github.com/minhnn/nexuslive-be/services/chat/internal/repo/persistent"
)

var (
	errEmoteUsage = errors.New("usage: chat emote add --channel <uuid> --tier 1-3 --code <code> --image </emotes/channel/x.webp> [--animated]" +
		" | remove --channel <uuid> --code <code> | seed-demo --channel <uuid>")
	errEmoteChannel = errors.New("--channel must be a uuid")
	errEmoteCode    = errors.New("--code must be 2-30 letters, digits or underscores")
	errEmoteImage   = errors.New("--image must be a path served by the edge under /emotes/channel/ or /media/")
	errEmoteMissing = errors.New("no such emote")
)

var (
	_emoteCodeRe  = regexp.MustCompile(`^[A-Za-z0-9_]{2,30}$`)
	_emoteImageRe = regexp.MustCompile(`^/(emotes/channel|media)/[A-Za-z0-9_\-./]+$`)
)

const (
	_tier1 = iota + 1
	_tier2
	_tier3
)

// demoEmotes are the channel emotes of `chat emote seed-demo` (images in deploy/emotes/channel/).
func demoEmotes() []emoteArgs {
	return []emoteArgs{
		{code: "kaelenGG", tier: _tier1},
		{code: "kaelenHi", tier: _tier1},
		{code: "kaelenHype", tier: _tier2},
		{code: "kaelenClutch", tier: _tier2},
		{code: "kaelenGOAT", tier: _tier3},
	}
}

type emoteArgs struct {
	channel, code, image string
	tier                 int
	animated             bool
}

// Emote manages channel emotes locked to a sub tier (an operator task: no NXL_ENV restriction). Images are
// files the edge serves; this command only records their path.
func Emote(cfg *config.Config, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errEmoteUsage
	}

	a, err := parseEmote(args[0], args[1:])
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

	return runEmote(ctx, persistent.New(pg), args[0], a, out)
}

func runEmote(ctx context.Context, r repo.EmoteRepo, cmd string, a emoteArgs, out io.Writer) error {
	switch cmd {
	case "add":
		return addEmote(ctx, r, a, out)
	case "remove":
		removed, err := r.RemoveChannelEmote(ctx, a.channel, a.code)
		if err != nil {
			return fmt.Errorf("remove: %w", err)
		}

		if !removed {
			return fmt.Errorf("%w: %s", errEmoteMissing, a.code)
		}

		_, err = fmt.Fprintf(out, "removed %s from %s\n", a.code, a.channel)

		return err
	}

	for _, d := range demoEmotes() {
		d.channel, d.image = a.channel, "/emotes/channel/"+d.code+".webp"
		if err := addEmote(ctx, r, d, out); err != nil {
			return err
		}
	}

	return nil
}

func addEmote(ctx context.Context, r repo.EmoteRepo, a emoteArgs, out io.Writer) error {
	channel, tier := a.channel, a.tier

	err := r.AddChannelEmote(ctx, entity.Emote{
		Code: a.code, ImageURL: a.image, Animated: a.animated, Scope: entity.EmoteChannel, ChannelID: &channel, TierRequired: &tier,
	})
	if err != nil {
		return fmt.Errorf("add %s: %w", a.code, err)
	}

	_, err = fmt.Fprintf(out, "ok: %s tier %d %s channel=%s\n", a.code, a.tier, a.image, a.channel)

	return err
}

func parseEmote(cmd string, args []string) (emoteArgs, error) {
	var a emoteArgs

	fs := flag.NewFlagSet("emote "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.channel, "channel", "", "channel id")
	fs.StringVar(&a.code, "code", "", "emote code")
	fs.StringVar(&a.image, "image", "", "image path served by the edge")
	fs.IntVar(&a.tier, "tier", 0, "sub tier required (1-3)")
	fs.BoolVar(&a.animated, "animated", false, "animated image")

	if err := fs.Parse(args); err != nil {
		return emoteArgs{}, fmt.Errorf("%w: %w", errEmoteUsage, err)
	}

	if _, err := uuid.Parse(a.channel); err != nil {
		return emoteArgs{}, errEmoteChannel
	}

	switch cmd {
	case "add":
		return a, validateEmote(a)
	case "remove":
		if !_emoteCodeRe.MatchString(a.code) {
			return emoteArgs{}, errEmoteCode
		}

		return a, nil
	case "seed-demo":
		return a, nil
	}

	return emoteArgs{}, errEmoteUsage
}

func validateEmote(a emoteArgs) error {
	switch {
	case !entity.ValidSubTier(a.tier):
		return errBadTier
	case !_emoteCodeRe.MatchString(a.code):
		return errEmoteCode
	case !_emoteImageRe.MatchString(a.image) || strings.Contains(a.image, ".."):
		return errEmoteImage
	}

	return nil
}
