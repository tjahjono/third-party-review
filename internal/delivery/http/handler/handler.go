// Package handler contains the HTTP handlers. Handlers translate requests into
// service calls and render templates; they hold no business logic and never
// touch a repository directly.
package handler

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"third-party-review/internal/delivery/http/middleware"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/service"

	"github.com/google/uuid"
)

type Handler struct {
	assessments service.AssessmentFacade
	reviews     service.ReviewService
	dashboards  service.DashboardService
	auth        service.AuthService
	settings    service.SettingsService
	templates   *Renderer
	sessionTTL  time.Duration
	log         *slog.Logger
}

func New(d Deps) (*Handler, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	ttl := d.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	return &Handler{
		assessments: d.Assessments,
		reviews:     d.Reviews,
		dashboards:  d.Dashboards,
		auth:        d.Auth,
		settings:    d.Settings,
		templates:   d.Templates,
		sessionTTL:  ttl,
		log:         d.Log,
	}, nil
}

// MustNew is New for wiring that cannot meaningfully recover, such as a test
// fixture.
func MustNew(d Deps) *Handler {
	h, err := New(d)
	if err != nil {
		panic(err)
	}
	return h
}

// defaultSessionTTL applies when none is configured.
const defaultSessionTTL = 12 * time.Hour

// Renderer parses and executes the template set. Each page is parsed together
// with the layout and every partial, so a partial can be rendered on its own
// for an HTMX swap or embedded in a full page render.
type Renderer struct {
	pages    map[string]*template.Template
	partials *template.Template
}

// NewRenderer parses templates from fsys at startup, so a broken template
// fails the build-and-deploy rather than one unlucky request.
func NewRenderer(fsys fs.FS) (*Renderer, error) {
	funcs := templateFuncs()

	partials, err := template.New("partials").Funcs(funcs).
		ParseFS(fsys, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse partials: %w", err)
	}

	pageFiles, err := fs.Glob(fsys, "templates/pages/*.html")
	if err != nil {
		return nil, fmt.Errorf("glob pages: %w", err)
	}
	if len(pageFiles) == 0 {
		return nil, errors.New("no page templates found")
	}

	pages := make(map[string]*template.Template, len(pageFiles))
	for _, page := range pageFiles {
		name := strings.TrimSuffix(fileBase(page), ".html")
		t, err := template.New("base.html").Funcs(funcs).ParseFS(fsys,
			"templates/layouts/base.html",
			"templates/partials/*.html",
			page,
		)
		if err != nil {
			return nil, fmt.Errorf("parse page %s: %w", name, err)
		}
		pages[name] = t
	}
	return &Renderer{pages: pages, partials: partials}, nil
}

// Page renders a full page through the base layout.
func (r *Renderer) Page(w io.Writer, name string, data any) error {
	t, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("unknown page template %q", name)
	}
	return t.ExecuteTemplate(w, "base.html", data)
}

// Partial renders a named partial on its own, for an HTMX swap.
func (r *Renderer) Partial(w io.Writer, name string, data any) error {
	return r.partials.ExecuteTemplate(w, name, data)
}

func fileBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// pageData is the envelope every full page render receives.
type pageData struct {
	Title   string
	Active  string
	Flash   string
	Error   string
	Data    any
	Domains []*model.AssessmentDomain

	// User is the signed-in reviewer, used by the layout's header.
	User *model.User
	// CSRFToken is embedded in every mutating form on the page.
	CSRFToken string
}

// renderPage writes a full page, logging and reporting any template failure.
// The signed-in user and CSRF token are filled in here so no individual
// handler can forget them - a missing token would fail every form on the page.
func (h *Handler) renderPage(w http.ResponseWriter, r *http.Request, status int, name string, data pageData) {
	if data.User == nil {
		data.User = middleware.UserFrom(r.Context())
	}
	if data.CSRFToken == "" {
		data.CSRFToken = middleware.CSRFTokenFrom(r.Context())
	}
	var buf strings.Builder
	if err := h.templates.Page(&buf, name, data); err != nil {
		h.log.Error("template render failed", "page", name, "error", err, "path", r.URL.Path)
		http.Error(w, "Something went wrong rendering this page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, buf.String())
}

// renderPartial writes a partial, used for HTMX swaps.
func (h *Handler) renderPartial(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	var buf strings.Builder
	if err := h.templates.Partial(&buf, name, data); err != nil {
		h.log.Error("partial render failed", "partial", name, "error", err, "path", r.URL.Path)
		http.Error(w, "Something went wrong rendering this section.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, buf.String())
}

// errorView is the data an error partial receives.
type errorView struct {
	Message string
	Hint    string
	Fields  []helper.ValidationError
}

// fail renders a user-facing error. Validation and parse problems become a 422
// with an explanatory partial; anything else is logged in full and reported
// generically, so an internal error never leaks a stack trace or a DSN into
// the browser.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, helper.ErrNotFound):
		h.renderPartial(w, r, http.StatusNotFound, "error", errorView{
			Message: "That record no longer exists.",
			Hint:    "It may have been deleted in another tab.",
		})
		return

	case errors.Is(err, helper.ErrInvalidInput):
		view := errorView{Message: err.Error()}

		var pe *helper.ParseError
		if errors.As(err, &pe) {
			view.Message, view.Hint = pe.Message, pe.Hint
		}
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			view.Message = ve.Message
			view.Fields = []helper.ValidationError{ve}
		}
		var ves helper.ValidationErrors
		if errors.As(err, &ves) {
			view.Message = "Please correct the highlighted fields."
			view.Fields = ves
		}
		h.renderPartial(w, r, http.StatusUnprocessableEntity, "error", view)
		return

	case errors.Is(err, helper.ErrAlreadyExists):
		h.renderPartial(w, r, http.StatusConflict, "error", errorView{
			Message: "That already exists.",
			Hint:    "Choose a different name.",
		})
		return
	}

	h.log.Error("request failed", "path", r.URL.Path, "method", r.Method, "error", err)
	h.renderPartial(w, r, http.StatusInternalServerError, "error", errorView{
		Message: "Something went wrong on our side.",
		Hint:    "The details were written to the server log.",
	})
}

// redirect sends the browser to a new URL, using the HTMX header when the
// request came from HTMX so the swap becomes a real navigation.
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, url string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// pathID reads a uuid path parameter.
//
// A malformed id is a ValidationError rather than a 404: the request never
// identified a record, so the honest answer is that the link is wrong, not
// that the thing it points at is missing.
func pathID(r *http.Request, name string) (uuid.UUID, error) {
	raw := strings.TrimSpace(chiURLParam(r, name))
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, helper.ValidationError{Field: name, Message: "That link is malformed."}
	}
	return id, nil
}

// formInt reads an integer form value, returning def when absent or unparseable.
func formInt(r *http.Request, name string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))
	if err != nil {
		return def
	}
	return v
}

// formID reads a uuid form value.
func formID(r *http.Request, name string) (uuid.UUID, bool) {
	v, err := uuid.Parse(strings.TrimSpace(r.FormValue(name)))
	if err != nil || v == uuid.Nil {
		return uuid.Nil, false
	}
	return v, true
}
