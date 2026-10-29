package ratefeed_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayorea/backend/internal/platform/ratefeed"
)

func serve(t *testing.T, status int, body string) *ratefeed.DolarAPI {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return ratefeed.NewDolarAPI(srv.URL)
}

func TestFetchReadsRateAndDay(t *testing.T) {
	feed := serve(t, 200, `{"moneda":"USD","fuente":"oficial","promedio":888.8909,"fechaActualizacion":"2026-10-29T00:00:00-04:00"}`)
	q, err := feed.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if q.Rate != 8_888_909 {
		t.Fatalf("rate = %d, want 8888909", q.Rate)
	}
	if want := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC); !q.On.Equal(want) {
		t.Fatalf("day = %v, want %v", q.On, want)
	}
}

func TestFetchRejectsBrokenAnswers(t *testing.T) {
	for name, feed := range map[string]*ratefeed.DolarAPI{
		"server error": serve(t, 500, `{}`),
		"not json":     serve(t, 200, `<html>`),
		"zero rate":    serve(t, 200, `{"promedio":0,"fechaActualizacion":"2026-10-29T00:00:00-04:00"}`),
		"missing rate": serve(t, 200, `{"fechaActualizacion":"2026-10-29T00:00:00-04:00"}`),
		"absurd rate":  serve(t, 200, `{"promedio":1e12,"fechaActualizacion":"2026-10-29T00:00:00-04:00"}`),
		"bad date":     serve(t, 200, `{"promedio":36.5,"fechaActualizacion":"ayer"}`),
	} {
		if _, err := feed.Fetch(context.Background()); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
