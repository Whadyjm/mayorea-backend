// Package ratefeed reads the official BCV dollar rate from DolarApi
// (https://ve.dolarapi.com), a free public feed.
package ratefeed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/mayorea/backend/internal/domain"
	"github.com/mayorea/backend/internal/usecase"
)

// DefaultURL is the official-rate endpoint of DolarApi.
const DefaultURL = "https://ve.dolarapi.com/v1/dolares/oficial"

type DolarAPI struct {
	URL    string
	Client *http.Client
}

func NewDolarAPI(url string) *DolarAPI {
	if url == "" {
		url = DefaultURL
	}
	return &DolarAPI{URL: url, Client: &http.Client{Timeout: 10 * time.Second}}
}

type payload struct {
	Average   float64 `json:"promedio"`
	UpdatedAt string  `json:"fechaActualizacion"`
}

// Fetch returns the rate (Bs per USD, ten-thousandths) and the day it applies to.
func (d *DolarAPI) Fetch(ctx context.Context) (usecase.RateQuote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return usecase.RateQuote{}, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := d.Client.Do(req)
	if err != nil {
		return usecase.RateQuote{}, fmt.Errorf("rate feed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return usecase.RateQuote{}, fmt.Errorf("rate feed: status %d", res.StatusCode)
	}
	var p payload
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&p); err != nil {
		return usecase.RateQuote{}, fmt.Errorf("rate feed: unreadable answer: %w", err)
	}
	// A rate of zero or an absurd one is a broken feed, never a price to bill with.
	if !(p.Average > 0 && p.Average < 1_000_000) {
		return usecase.RateQuote{}, fmt.Errorf("rate feed: implausible rate %v", p.Average)
	}
	at, err := time.Parse(time.RFC3339, p.UpdatedAt)
	if err != nil {
		return usecase.RateQuote{}, fmt.Errorf("rate feed: bad date %q", p.UpdatedAt)
	}
	y, m, day := at.Date() // the day in the feed's own zone (Venezuela)
	return usecase.RateQuote{
		Rate: domain.Rate(math.Round(p.Average * 10_000)),
		On:   time.Date(y, m, day, 0, 0, 0, 0, time.UTC),
	}, nil
}
