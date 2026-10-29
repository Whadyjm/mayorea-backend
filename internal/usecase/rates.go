package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mayorea/backend/internal/domain"
)

// RateQuote is a rate published by an outside source for a business day.
type RateQuote struct {
	Rate domain.Rate
	On   time.Time // the day it applies to, as UTC midnight
}

// RateSource is where the official rate is read from.
type RateSource interface {
	Fetch(ctx context.Context) (RateQuote, error)
}

// RateService keeps the stored BCV rate in step with the official source.
type RateService struct {
	rates  RateRepository
	source RateSource
	clock  Clock
}

func NewRateService(r RateRepository, s RateSource, c Clock) *RateService {
	return &RateService{rates: r, source: s, clock: c}
}

// Sync reads the official rate and stores it when it is news. A rate someone typed
// in by hand for a day is respected until the feed publishes a later day; a rate
// that came from the feed is replaced whenever the feed corrects it. It reports
// whether the stored rate changed.
func (s *RateService) Sync(ctx context.Context) (bool, error) {
	q, err := s.source.Fetch(ctx)
	if err != nil {
		return false, err
	}
	cur, err := s.rates.Current(ctx)
	var nf *domain.Error
	switch {
	case err == nil:
	case errors.As(err, &nf) && nf.Kind == domain.KindNotFound:
		cur = domain.ExchangeRate{}
	default:
		return false, err
	}

	news := cur.Rate == 0 ||
		q.On.After(cur.UpdatedOn) ||
		(q.On.Equal(cur.UpdatedOn) && cur.Source == domain.RateSourceFeed && q.Rate != cur.Rate)
	if !news {
		return false, nil
	}
	if err := s.rates.Set(ctx, domain.ExchangeRate{Rate: q.Rate, UpdatedOn: q.On, UpdatedAt: s.clock.Now(), Source: domain.RateSourceFeed}); err != nil {
		return false, err
	}
	return true, nil
}

// Run syncs now and then every interval until ctx ends. A failed read only
// logs: the last stored rate keeps serving.
func (s *RateService) Run(ctx context.Context, interval time.Duration) {
	tick := func() {
		changed, err := s.Sync(ctx)
		switch {
		case ctx.Err() != nil:
		case err != nil:
			slog.Warn("bcv rate sync failed", "err", err)
		case changed:
			if cur, err := s.rates.Current(ctx); err == nil {
				slog.Info("bcv rate updated", "rate", cur.Rate, "day", cur.UpdatedOn.Format("2006-01-02"))
			}
		}
	}
	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
