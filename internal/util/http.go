// Package util provides small cross-cutting utilities used across the application.
package util

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog"
)

// HandlerFunc is a generic http.Handler that decodes a JSON request body into
// I, invokes the wrapped function, and encodes the returned *O as a JSON
// response body. Errors returned by the function are surfaced as 400s;
// decode/encode failures are surfaced as 500s.
//
// Typical usage pattern:
//
//	func Create(pool *sqlitex.Pool) util.HandlerFunc[CreateRequest, CreateResponse] {
//	    return func(ctx context.Context, req CreateRequest) (*CreateResponse, error) {
//	        // ... business logic ...
//	    }
//	}
//
//	r.Method(http.MethodPost, "/api/things", Create(pool))
type HandlerFunc[I any, O any] func(context.Context, I) (*O, error)

func (h HandlerFunc[I, O]) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	log := zerolog.Ctx(req.Context())

	var input I
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		log.Error().Err(err).Msg("failed to decode request body")
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}

	out, err := h(req.Context(), input)
	if err != nil {
		log.Error().Err(err).Send()
		http.Error(res, err.Error(), http.StatusBadRequest)
		return
	}

	res.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(res).Encode(out); err != nil {
		log.Error().Err(err).Msg("failed to encode response body")
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}
}
