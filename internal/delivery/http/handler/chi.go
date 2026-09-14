package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// chiURLParam is a thin indirection over chi so the rest of the handler
// package does not import the router directly, keeping handlers testable with
// a plain http.Request.
func chiURLParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}
