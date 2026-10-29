package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mayorea/backend/internal/adapter/memory"
	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/platform/clock"
	"github.com/mayorea/backend/internal/usecase"
)

type fakeFeed struct {
	q   usecase.RateQuote
	err error
}

func (f *fakeFeed) Fetch(context.Context) (usecase.RateQuote, error) { return f.q, f.err }

func octDay(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }

func TestRateSyncPolicy(t *testing.T) {
	defer catch(t)
	ctx := context.Background()
	st := memory.NewStore()
	clk := &clock.Fixed{Day: octDay(29)}
	feed := &fakeFeed{q: usecase.RateQuote{Rate: 8_888_909, On: octDay(29)}}
	svc := usecase.NewRateService(st.Rates(), feed, clk)
	catalog := usecase.NewCatalogService(st.Products(), st.Lots(), st.Customers(), st.Trucks(), st.Rates(), clk)

	// Nothing stored yet: the feed's rate is taken.
	changed := must(svc.Sync(ctx))
	cur := must(catalog.CurrentRate(ctx))
	if !changed || cur.Rate != 8_888_909 || cur.Source != domain.RateSourceFeed || !cur.UpdatedOn.Equal(octDay(29)) {
		t.Fatalf("first sync: changed=%v cur=%+v", changed, cur)
	}

	// Same day and value: nothing to do. Same day, corrected by the feed: replaced.
	if must(svc.Sync(ctx)) {
		t.Fatal("an unchanged rate must not be rewritten")
	}
	feed.q.Rate = 8_900_000
	if !must(svc.Sync(ctx)) || must(catalog.CurrentRate(ctx)).Rate != 8_900_000 {
		t.Fatal("a feed correction for the same day must replace the feed's own value")
	}

	// A rate typed by hand today survives the feed until the feed moves to a later day.
	must(catalog.SetRate(ctx, 9_000_000))
	feed.q.Rate = 8_950_000
	if must(svc.Sync(ctx)) || must(catalog.CurrentRate(ctx)).Rate != 9_000_000 {
		t.Fatal("a manual rate must not be overwritten on the same day")
	}
	feed.q = usecase.RateQuote{Rate: 9_100_000, On: octDay(30)}
	if !must(svc.Sync(ctx)) {
		t.Fatal("a later day must replace even a manual rate")
	}
	if cur := must(catalog.CurrentRate(ctx)); cur.Rate != 9_100_000 || cur.Source != domain.RateSourceFeed || !cur.UpdatedOn.Equal(octDay(30)) {
		t.Fatalf("after the new day: %+v", cur)
	}

	// An older day never overwrites a newer one.
	feed.q = usecase.RateQuote{Rate: 1, On: octDay(28)}
	if must(svc.Sync(ctx)) {
		t.Fatal("an older day must be ignored")
	}

	// A broken feed leaves the stored rate alone.
	feed.err = errors.New("boom")
	if _, err := svc.Sync(ctx); err == nil {
		t.Fatal("expected the feed error")
	}
	if must(catalog.CurrentRate(ctx)).Rate != 9_100_000 {
		t.Fatal("a failed sync must keep the stored rate")
	}
}
