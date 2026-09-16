// Package http wires handlers, middleware and static assets into a router.
package http

import (
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"third-party-review/internal/delivery/http/handler"
	"third-party-review/internal/delivery/http/middleware"
	"third-party-review/internal/service/auth"
)

// Deps is everything the router needs, injected by the main package.
type Deps struct {
	// Handler is the delivery contract rather than the concrete handler set,
	// so the routing table can be tested against a stub.
	Handler       handler.Routes
	Auth          *auth.Service
	SessionSecret string
	Static        fs.FS
	Health        func() error
	Log           *slog.Logger
}

// NewRouter builds the application router.
//
// The shape of this file is the security boundary: everything that touches
// assessment data lives inside the authenticated group, and the only routes
// outside it are the health check, static assets, and the login flow itself.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recover(d.Log))
	r.Use(middleware.Logger(d.Log))
	r.Use(middleware.SecurityHeaders)
	r.Use(chimw.Compress(5))
	// Long-running AI work happens in the background worker, so no HTTP
	// request needs more than a few seconds.
	r.Use(chimw.Timeout(30 * time.Second))
	// Resolve the session for every request, including the public ones, so a
	// signed-in user visiting /login is redirected rather than shown a form.
	r.Use(middleware.Auth(d.Auth, d.SessionSecret))
	r.Use(middleware.CSRF(d.Log))

	h := d.Handler

	// ---- public ------------------------------------------------------------

	r.Get("/healthz", healthHandler(d.Health))

	// Static assets are embedded in the binary, so the image needs no volume
	// and there is no chance of serving a stale asset after a deploy.
	staticServer := http.StripPrefix("/static/", http.FileServer(http.FS(d.Static)))
	r.Handle("/static/*", cacheStatic(staticServer))

	r.Get("/login", h.LoginPage)
	r.Post("/login", h.Login)
	r.Get("/login/mfa", h.MFAPage)
	r.Post("/login/mfa", h.VerifyMFA)
	r.Post("/setup", h.FirstRunSetup)
	r.Post("/logout", h.Logout)

	// ---- authenticated -----------------------------------------------------

	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth("/login"))

		r.Get("/", h.DashboardPage)

		r.Route("/account", func(r chi.Router) {
			r.Get("/", h.AccountPage)
			r.Post("/password", h.ChangePassword)
			r.Post("/mfa/begin", h.BeginMFA)
			r.Post("/mfa/confirm", h.ConfirmMFA)
			r.Post("/mfa/disable", h.DisableMFA)
			r.Post("/mfa/recovery-codes", h.RegenerateRecoveryCodes)
		})

		r.Route("/vendors", func(r chi.Router) {
			r.Get("/", h.ListVendors)
			r.Post("/", h.CreateVendor)
			r.Delete("/{vendorID}", h.DeleteVendor)
		})

		r.Route("/assessments", func(r chi.Router) {
			r.Get("/", h.ListAssessments)
			r.Get("/new", h.NewAssessment)
			r.Post("/", h.UploadAssessment)

			r.Route("/{assessmentID}", func(r chi.Router) {
				r.Get("/", h.AssessmentDetail)
				r.Delete("/", h.DeleteAssessment)
				r.Get("/export.csv", h.ExportCSV)

				// Ingestion
				r.Get("/mapping", h.MappingPage)
				r.Post("/mapping/preview", h.RepreviewMapping)
				r.Post("/mapping", h.ConfirmMapping)

				// Review
				r.Post("/review", h.StartReview)
				r.Get("/review/status", h.ReviewStatus)
				r.Get("/questions", h.QuestionList)
				r.Get("/summary", h.SummaryPanel)

				// Human sign-off
				r.Get("/signoff", h.SignOffProgress)
				r.Post("/finalize-all", h.BulkFinalize)
				r.Post("/close", h.CloseAssessment)
				r.Post("/reopen", h.ReopenAssessment)

				// Rubric
				r.Get("/rubric", h.RubricPanel)
				r.Post("/rubric", h.AttachRubric)
				r.Put("/rubric", h.UpdateRubric)
				r.Delete("/rubric", h.DetachRubric)
				r.Get("/rubric/edit", h.EditRubric)
				r.Get("/rubric/cancel", h.CancelEditRubric)
			})
		})

		r.Route("/questions/{questionID}", func(r chi.Router) {
			r.Get("/edit", h.EditQuestion)
			r.Get("/cancel", h.CancelEditQuestion)
			r.Post("/finalize", h.FinalizeQuestion)
			r.Post("/reopen", h.ReopenQuestion)
		})
	})

	return r
}

// healthHandler reports readiness, including database reachability, so a
// container orchestrator restarts an app that has lost its database.
func healthHandler(check func() error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			if err := check(); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"unhealthy","reason":"database unreachable"}`))
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

// cacheStatic sets a short cache lifetime on embedded assets.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}
