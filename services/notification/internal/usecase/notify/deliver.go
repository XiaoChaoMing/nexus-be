package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/minhnn/nexuslive-be/services/notification/internal/entity"
	"github.com/minhnn/nexuslive-be/services/notification/internal/repo"
	"github.com/minhnn/nexuslive-be/services/notification/internal/usecase"
)

// Keys of the e-mail template data.
const (
	keyName    = "Name"
	keyLink    = "Link"
	keyChannel = "Channel"
	keyTitle   = "Title"
	keyReason  = "Reason"
	keyUnsub   = "UnsubscribeURL"
)

// spec describes the notification one source event produces for each recipient. The rules apply in the
// order of NXL-E (AI rule 5): valid recipient → settings → block (mention) → cooldown / grouping / daily cap.
type spec struct {
	n entity.Notification
	// template is the e-mail template ("" = in-app only).
	template string
	// data builds the template data of one recipient (links absolute; name and unsubscribe link are added).
	data func(r entity.Recipient) map[string]string
	// liveChannel turns on the CREATOR_LIVE cooldowns of that channel (BR-E11).
	liveChannel string
}

func (s spec) forced() bool { return entity.DefaultOf(s.n.Type).EmailForced }

// emailFor builds the e-mail of a recipient, with the unsubscribe link of non-mandatory types.
func (uc *UseCase) emailFor(s spec, r entity.Recipient) entity.Email {
	data := s.data(r)
	data[keyName] = r.Name()

	if !s.forced() {
		data[keyUnsub] = uc.unsubscribeURL(r.UserID, s.n.Type)
	}

	return entity.Email{
		UserID: r.UserID, To: r.Email, Template: s.template, Locale: locale(r.Locale), Data: data, Mandatory: s.forced(),
	}
}

type cooldowns struct {
	inApp, email map[string]string
}

// blocks reports whether a cooldown key holds another event than this one (the same id = a redelivery).
func blocks(holder, eventID string) bool { return holder != "" && holder != eventID }

// gate holds what the rules need about one batch of recipients.
type gate struct {
	eventID   string
	overrides map[string]map[entity.Type]entity.Setting
	cool      cooldowns
	today     map[string]int
}

func (g gate) setting(t entity.Type, userID string) entity.Setting {
	var o *entity.Setting
	if v, ok := g.overrides[userID][t]; ok {
		o = &v
	}

	return entity.Effective(t, o)
}

// choose applies settings, the live cooldowns, the verified-only rule and the daily cap; it returns whether
// the recipient gets the in-app row and the e-mail.
func (uc *UseCase) choose(s spec, r entity.Recipient, g gate) (inApp, email bool) {
	t := s.n.Type
	set := g.setting(t, r.UserID)
	inApp, email = set.InApp, s.template != "" && set.Email

	if !inApp && !email {
		uc.obs.Suppressed(t, usecase.SuppressSettings)

		return false, false
	}

	if inApp && blocks(g.cool.inApp[r.UserID], g.eventID) {
		inApp = false

		uc.obs.Suppressed(t, usecase.SuppressCooldown)
	}

	return inApp, email && uc.emailAllowed(s, r, g)
}

func (uc *UseCase) emailAllowed(s spec, r entity.Recipient, g gate) bool {
	t := s.n.Type

	switch {
	case !r.EmailVerified:
		uc.obs.Suppressed(t, usecase.SuppressNoEmail)
	case blocks(g.cool.email[r.UserID], g.eventID):
		uc.obs.Suppressed(t, usecase.SuppressCooldown)
	case !s.forced() && g.today[r.UserID] >= uc.cfg.EmailDailyMax:
		uc.obs.Suppressed(t, usecase.SuppressDailyCap)
	default:
		return true
	}

	return false
}

// newGate loads the settings, cooldowns and daily counts of a batch; days maps users to their day key.
func (uc *UseCase) newGate(ctx context.Context, eventID string, s spec, recipients []entity.Recipient,
) (gate, map[string]string, error) {
	g := gate{eventID: eventID}

	ids := make([]string, 0, len(recipients))
	for _, r := range recipients {
		ids = append(ids, r.UserID)
	}

	var err error

	if g.overrides, err = uc.r.Settings.Overrides(ctx, ids); err != nil {
		return g, nil, fmt.Errorf("notify - overrides: %w", err)
	}

	if s.liveChannel != "" {
		if g.cool.inApp, g.cool.email, err = uc.r.Throttle.LiveCooldowns(ctx, s.liveChannel, ids); err != nil {
			return g, nil, fmt.Errorf("notify - cooldowns: %w", err)
		}
	}

	days, today, err := uc.emailsToday(ctx, s, recipients)
	g.today = today

	return g, days, err
}

// deliverBatch writes one fan-out batch (BR-E14: unique (user_id, source_event_id), no processed_events).
func (uc *UseCase) deliverBatch(ctx context.Context, eventID string, s spec, recipients []entity.Recipient) error {
	if len(recipients) == 0 {
		return nil
	}

	g, days, err := uc.newGate(ctx, eventID, s, recipients)
	if err != nil {
		return err
	}

	var (
		inApp, emailed []string
		emails         []entity.Email
	)

	for _, r := range recipients {
		app, mail := uc.choose(s, r, g)
		if app {
			inApp = append(inApp, r.UserID)
		}

		if mail {
			emails = append(emails, uc.emailFor(s, r))
			emailed = append(emailed, r.UserID)
		}
	}

	res, err := uc.r.Notifications.InsertBatch(ctx, eventID, s.n, inApp, emails)
	if err != nil {
		return fmt.Errorf("notify - batch - insert: %w", err)
	}

	uc.setCooldowns(ctx, s, eventID, inApp, emailed)
	uc.countEmails(ctx, s, res.Emailed, days)

	for range res.Notified {
		uc.obs.Created(s.n.Type, false)
	}

	return nil
}

// setCooldowns starts the live cooldowns, for redeliveries too: the value is this event's id, which never
// blocks this event.
func (uc *UseCase) setCooldowns(ctx context.Context, s spec, eventID string, inApp, emailed []string) {
	if s.liveChannel == "" || len(inApp)+len(emailed) == 0 {
		return
	}

	err := uc.r.Throttle.SetLiveCooldowns(ctx, s.liveChannel, eventID, inApp, emailed, uc.cfg.LiveCooldown,
		uc.cfg.LiveEmailCooldown)
	if err != nil {
		uc.l.Warn("notify - live cooldowns %s: %v", eventID, err)
	}
}

// emailsToday reads the daily counts of the recipients that may get a non-mandatory e-mail.
func (uc *UseCase) emailsToday(ctx context.Context, s spec, recipients []entity.Recipient,
) (days map[string]string, today map[string]int, err error) {
	if s.template == "" || s.forced() {
		return nil, nil, nil
	}

	days = make(map[string]string, len(recipients))
	for _, r := range recipients {
		if r.EmailVerified {
			days[r.UserID] = uc.dayKey(r)
		}
	}

	today, err = uc.r.Throttle.EmailsToday(ctx, days)
	if err != nil {
		return nil, nil, fmt.Errorf("notify - emails today: %w", err)
	}

	return days, today, nil
}

// countEmails adds the queued non-mandatory e-mails to the daily counts; a Redis error only loosens the cap.
func (uc *UseCase) countEmails(ctx context.Context, s spec, emailed []string, days map[string]string) {
	if s.forced() || len(emailed) == 0 {
		return
	}

	counted := make(map[string]string, len(emailed))

	for _, id := range emailed {
		if d, ok := days[id]; ok {
			counted[id] = d
		}
	}

	if err := uc.r.Throttle.CountEmails(ctx, counted); err != nil {
		uc.l.Warn("notify - count emails: %v", err)
	}
}

// deliverSmall writes the notifications of an event with few recipients in one transaction, grouping when
// the spec has a group key (BR-E12, BR-E14).
func (uc *UseCase) deliverSmall(ctx context.Context, eventID string, s spec, window time.Duration,
	recipients []entity.Recipient,
) error {
	if len(recipients) == 0 {
		return nil
	}

	g, days, err := uc.newGate(ctx, eventID, s, recipients)
	if err != nil {
		return err
	}

	items := make([]repo.Pending, 0, len(recipients))

	for _, r := range recipients {
		if p, ok := uc.pending(s, r, g, window); ok {
			items = append(items, p)
		}
	}

	if len(items) == 0 {
		return nil
	}

	done, err := uc.r.Notifications.Upsert(ctx, eventID, items)
	if err != nil {
		return fmt.Errorf("notify - small - upsert: %w", err)
	}

	var emailed []string

	for _, u := range done {
		if u.Emailed {
			emailed = append(emailed, u.Notification.UserID)
		}

		if u.Notification.ID != "" {
			uc.obs.Created(s.n.Type, u.Grouped)
		}
	}

	uc.countEmails(ctx, s, emailed, days)

	return nil
}

func (uc *UseCase) pending(s spec, r entity.Recipient, g gate, window time.Duration) (repo.Pending, bool) {
	app, mail := uc.choose(s, r, g)
	if !app && !mail {
		return repo.Pending{}, false
	}

	n := s.n
	n.UserID = r.UserID

	p := repo.Pending{Notification: n, Window: window, EmailOnly: !app}

	if mail {
		e := uc.emailFor(s, r)
		p.Email = &e
	}

	return p, true
}
