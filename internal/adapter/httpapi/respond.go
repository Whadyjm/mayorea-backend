package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/mayorea/backend/internal/domain"
)

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError translates domain error kinds to HTTP statuses. Anything that is
// not a domain error is logged and hidden behind a generic 500.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	msg := "Algo salió mal de nuestro lado. Intenta de nuevo."
	switch domain.KindOf(err) {
	case domain.KindInvalid:
		status = http.StatusBadRequest
	case domain.KindNotFound:
		status = http.StatusNotFound
	case domain.KindConflict:
		status = http.StatusConflict
	case domain.KindRule:
		status = http.StatusUnprocessableEntity
	case domain.KindUnauthenticated:
		status = http.StatusUnauthorized
	case domain.KindForbidden:
		status = http.StatusForbidden
	case domain.KindThrottled:
		status = http.StatusTooManyRequests
	}
	if status == http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	} else {
		msg = err.Error()
	}
	writeJSON(w, status, errorBody{msg})
}

func decode(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return domain.Invalid("El cuerpo de la petición no es un JSON válido.")
		}
		return domain.Invalid("Datos inválidos: %v", err)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.Invalid("Identificador inválido.")
	}
	return id, nil
}

func queryInt(r *http.Request, name string) (int64, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 0, domain.Invalid("El parámetro %s es inválido.", name)
	}
	return v, nil
}
