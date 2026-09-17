# TPSA Reviewer

An internal tool for managing **Third Party Security Assessments**. Upload a
vendor's completed security questionnaire (Excel or CSV), and an AI reviewer
drafts a risk score, flags, completeness judgement and assessor feedback for
every answer. A human reads, edits and signs off every draft — the AI never has
the last word.

Single-tenant by design: one internal team, one login, no org/workspace concept.

---

## Status

All phases are implemented and tested.

| Phase | Scope | State |
|---|---|---|
| 0 | Scaffolding, config, Docker Compose, migrations | Done |
| 1 | Domain types, schema, seeded domains, Postgres repositories | Done |
| 2 | Excel/CSV parsing, column mapping, domain detection, ingestion UI | Done |
| 3 | Provider-agnostic AI review, background jobs, scoring, rubric | Done |
| 4 | Inline draft editing, sign-off, close, summary view | Done |
| 5 | Session login with optional TOTP MFA, CSV export, polish | Done |

Every route that touches assessment data sits behind authentication, and every
state-changing request is CSRF-checked.

---

## Running it

### Requirements

Docker with Compose v2. That is the whole list — Go, Postgres and the migration
tool all live inside the build. To run it without Docker, see below; that path
needs Go 1.25.4 and a Postgres you point it at.

### With Docker (the intended path)

Credentials (SESSION_SECRET, the Postgres password, AI_API_KEY,
BOOTSTRAP_PASSWORD) are supplied as Docker secrets, not plain environment
variables — see `secrets/README.md` for the full explanation. Everything
else lives in `.env`, same as before.

Run everything **from the repository root**:

```bash
make env                      # creates .env from .env.example
make secrets                  # creates secrets/*.txt (generates SESSION_SECRET;
                               #   edit the rest - postgres/AI/bootstrap - before deploying)
make up                       # builds and starts app + postgres
make logs                     # follow the app log
```

Then open <http://localhost:8080>. The first visit shows a setup screen to
create your account.

Without `make`, the equivalent is:

```bash
cp .env.example .env
mkdir -p secrets
openssl rand -hex 32 > secrets/session_secret.txt
echo -n 'a-strong-postgres-password'  > secrets/postgres_password.txt
echo -n ''                            > secrets/ai_api_key.txt   # or your key
echo -n 'a-strong-bootstrap-password' > secrets/bootstrap_password.txt
docker compose -f docker/docker-compose.yml --env-file .env up -d --build
```

`--env-file .env` is not optional. Compose resolves a bare `.env` relative to
the **compose file's** directory (`docker/`), not the repository root, so
without it the `.env` you just created is silently ignored and every setting
falls back to its default.

Migrations run automatically on startup, including the seed of the 8 TPSA
domains. Nothing else to initialise.

> **Upgrading from a build before the uuid change:** migration `000001` was
> rewritten rather than added to, so the schema a running database already has
> does not match it. Run `make reset` once to drop the volume and let it
> rebuild. This deletes existing assessments — there is no migration path from
> the old bigint keys, and the project has no production data to preserve.

Useful targets: `make logs`, `make down` (stop, keep the data),
`make reset` (stop and delete the database volume), `make psql`.
`make help` lists them all.

### With Docker Swarm (`docker stack deploy`)

The same `docker/docker-compose.yml` deploys to a Swarm, with one difference:
Swarm ignores the `build:` block, so build and tag the image yourself first.
`make stack-deploy` does both steps:

```bash
make env                      # if you haven't already
make secrets                  # if you haven't already
docker swarm init              # skip if this host is already a swarm manager
make stack-deploy              # builds+tags the image, then `docker stack deploy`
```

Or by hand:

```bash
docker build -f docker/Dockerfile -t tpsa-reviewer-app:latest .
docker stack deploy -c docker/docker-compose.yml tpsa-reviewer
```

Run `docker stack deploy` **from the repository root**: unlike `docker
compose`, it has no `--env-file` flag — it resolves a bare `.env` from the
current directory on its own. The four secret files are read once at deploy
time and turned into encrypted Swarm secret objects; editing a `secrets/*.txt`
file afterwards does not change what's already running — see
`secrets/README.md` for how to rotate one.

Check on it with `docker stack services tpsa-reviewer` and
`docker service logs -f tpsa-reviewer_app`. `make stack-rm` tears the stack
down (the `postgres-data` volume is kept).

This setup targets a single-node swarm, which is what an internal tool like
this one almost always runs on. Multi-node needs more than credential
handling — shared storage for the `postgres-data` volume, and the Postgres
port publish in `docker-compose.yml` (localhost-only, for `psql` during
development) reconsidered, since Swarm's routing mesh does not honour a
host-IP-scoped port binding the same way across nodes.

### Without Docker

Needs Go 1.25.4 (the version pinned in `go.mod`) and a reachable Postgres 13+
(`gen_random_uuid()` is built in from 13).

```bash
createdb tpsa
export DATABASE_URL="postgres://you@localhost:5432/tpsa?sslmode=disable"
export SESSION_SECRET="$(openssl rand -hex 32)"
export AI_PROVIDER=mock
make run        # or: go run ./cmd/server
```

### First account

The first visit shows a setup screen. To create the account
non-interactively instead — for a scripted deployment — set
`BOOTSTRAP_USERNAME` in `.env` and `BOOTSTRAP_PASSWORD` (under Docker,
`secrets/bootstrap_password.txt` — see `secrets/README.md`). Both are
ignored once any account exists, so a restart can never resurrect or reset
an account.

### Trying it without an AI provider

`AI_PROVIDER=mock` (the default in `.env.example`) runs the entire pipeline
with a deterministic offline reviewer — no API key, no cost, no network. Upload
a questionnaire, map it, run a review, sign off and export, all before you
point it at a real model. Switch to your own provider when you are ready; see
the next section.

---

## Configuring the AI provider

The reviewer is reached through one interface (`aiclient.AIReviewer`) with
three implementations. `AI_PROVIDER` selects one; nothing above the client
layer knows which is active.

### Open WebUI serving Qwen (the default deployment)

Open WebUI speaks the OpenAI chat-completions shape, so it uses the
`openaicompat` client. The base URL is the Open WebUI root plus `/api`; the API
key comes from **Settings → Account → API keys**.

```env
AI_PROVIDER=openaicompat
AI_BASE_URL=https://openwebui.internal.example/api
AI_CHAT_PATH=/chat/completions
AI_API_KEY=sk-...
AI_MODEL=qwen2.5:32b-instruct
```

The same client covers OpenAI (`AI_BASE_URL=https://api.openai.com/v1`), vLLM,
Ollama's compatibility layer, LiteLLM and most internal gateways.

### Anthropic

```env
AI_PROVIDER=anthropic
AI_BASE_URL=https://api.anthropic.com/v1
AI_API_KEY=sk-ant-...
AI_MODEL=claude-sonnet-4-20250514
```

### Mock

`AI_PROVIDER=mock` is a deterministic offline reviewer. It applies a few of the
heuristics a real assessor would (blank answer → score 5 and a missing-answer
flag; "industry standard firewalls are in place" → vague flag) so the entire
pipeline can be exercised with no API key and no cost. It does not read for
meaning and is not a substitute for a model.

### Notes on self-hosted backends

Strict structured output is not universally available — an Open WebUI proxying
to Ollama or vLLM may ignore `response_format` entirely. The client therefore
requests JSON, but never depends on getting it:

- markdown fences, prose preambles and `<think>` blocks are stripped;
- the JSON is located with a bracket-stack scan that respects strings, so a
  rationale quoting `"{not applicable}"` does not truncate the object;
- output cut off at the token limit is repaired by closing what is open, so the
  complete prefix of a batch is still usable;
- a 400 rejecting `response_format` retries once without it;
- an unparseable reply gets one corrective round trip.

If reviews come back truncated, raise `AI_MAX_TOKENS` or lower
`AI_BATCH_SIZE` — a smaller batch means fewer answers per call, at the cost of
some cross-answer contradiction detection.

---

## How a review runs

1. **Upload** — the file is stored against a new assessment in `uploaded`. It is
   kept so the mapping can be revisited without a re-upload, and so an
   ingestion decision stays auditable against the source.
2. **Map & confirm** — the header row is located, the 7 template columns are
   fuzzy-matched and pre-filled, and domain section boundaries are detected.
   You correct anything wrong and commit → `mapped`.
3. **Attach a rubric** (optional) — paste or upload a policy; answers are then
   compared against it and gaps flagged.
4. **Review** — a background job reviews each domain as a batch (so the model
   can see two answers contradict each other), with a per-question follow-up
   for anything the batch dropped or scored with low confidence. Progress is
   polled over HTMX → `reviewed`.
5. **Sign off** — a reviewer opens each question's draft in an inline editor,
   edits it if needed, and signs it. "Accept all remaining drafts" signs the
   rest as written, for the common case of reading through a domain and
   agreeing with it.
6. **Close** — once every question is signed, the assessment can be closed. It
   becomes read-only and the stored upload is discarded; the questions and the
   confirmed mapping are the record. Reopening is one click.

### Scoring

Each answer gets an integer **1–5** residual-risk score; the UI shows a band
derived from it (1–2 Low, 3 Medium, 4 High, 5 Critical).

The assessment-level score is a **weighted worst-case** aggregate, not a mean.
A straight average is the wrong summary here: a vendor with seventeen solid
answers and three unanswered questions averages to 1.6 — "low risk" — which is
exactly the conclusion the assessment exists to prevent. The aggregate blends
the mean with the mean of the worst quintile, weighted 60% to the latter, and
is computed in Go rather than by the model so the figures are deterministic,
explainable and identical across providers.

### Draft → sign-off

`assessor_feedback_draft` is written by the AI and **never** overwritten. When
a reviewer signs a question, their text goes to `assessor_feedback_final` and
the row records who signed it and when. Both columns are kept, so what the AI
said and what a person put their name to are always separable.

Re-running a review writes new `review_results` rows keyed to a new run —
earlier results are preserved — and will not reopen a question a human has
already signed.

Bulk sign-off accepts drafts *as written* and refuses to sign a question the AI
left without a draft: an empty finding that reads as reviewed is worse than an
obviously unreviewed one. Those questions are counted separately and the
assessment cannot be closed until they are written by hand.

### Export

`Export CSV` produces 15 columns: the original questionnaire fields, the AI's
score, band, completeness, flags and rationale, and the effective feedback —
final where signed, the draft otherwise. A **Feedback Status** column labels
every row `Signed off`, `UNCONFIRMED AI DRAFT - not signed off`, or
`NOT REVIEWED - no feedback`. An export that silently mixed signed findings with
unreviewed AI text would be the most dangerous artefact this tool could produce,
because it reads as a finished assessment.

---

## Authentication

One internal team, one login, no roles — everyone who can sign in can do
everything. Passwords are bcrypt (cost 12) with a 12-character floor and no
composition rules; length is the property that actually costs an attacker work,
whereas composition rules push people towards predictable substitutions.
Sessions are server-side rows keyed by a 256-bit random cookie value, so a
restart does not sign the team out and a logout genuinely ends the session.

Failed logins are deliberately indistinguishable: an unknown username hashes a
throwaway password before failing, so it takes the same time as a wrong one and
the form cannot be used to enumerate accounts.

### Optional two-factor

MFA is **per account and entirely optional** — a team that does not want it
never sees it. A user who turns it on scans a QR code with any authenticator
app and is issued eight single-use recovery codes, shown once and stored
bcrypt-hashed.

TOTP (RFC 6238) is implemented in `pkg/totp` rather than pulled in as a
dependency: the whole algorithm is about eighty lines of standard library, and
an auth primitive is worth being able to read end to end. It is tested against
the RFC's published reference vectors. Code comparison is constant-time and the
verification loop does not exit early, so a match takes the same time wherever
in the window it occurs.

Details that matter in practice:

- The secret is **not stored** until the user proves they can generate a code
  from it, so a half-finished enrolment cannot lock anyone out.
- A mistyped confirmation code re-renders the *same* secret rather than issuing
  a new one — swapping the secret under someone who has already scanned it is
  maddening.
- A one-period skew either side is accepted, which tolerates a phone clock up
  to thirty seconds out without meaningfully widening the window.
- The session between password and code is pending for five minutes and
  authorises nothing.
- Turning MFA off, or reissuing recovery codes, requires the password again.

### CSRF

Every state-changing request carries a token derived from the session id and
the application secret — nothing extra to store or expire. It is attached once
via `hx-headers` on `<body>` and inherited by every HTMX request on the page,
so no individual control can forget it; plain forms carry it as a hidden field.
Safe methods and anonymous requests pass through, because there is no session
to forge against.

The session cookie is `HttpOnly`, `SameSite=Lax`, and `Secure` only when the
request actually arrived over TLS — hardcoding `Secure` would make the cookie
silently vanish on the plain-HTTP internal deployment this tool starts life in.

## Ingestion

The known 8-domain / 7-column template is treated as a strong prior, not a
requirement.

**Columns** are matched by a blend of character distance and token overlap, so
`3rd Party Answer` matches `Third Party Answer` and `Link Evidence` matches
`Evidence Link`. Assignment is resolved globally rather than per column, so a
file carrying both *Assessor Remark* and *Assessor Feedback* gets each its own
field. A match below 70% is left unmapped rather than guessed — an empty
dropdown is a smaller correction than a wrong one the user has to spot first.
If no column is recognisably *Question*, the longest-prose column is offered as
a flagged guess; if even that fails, the mapping screen still loads so the
column can be chosen by hand.

**Domains** are section dividers inside one sheet, not a column or separate
tabs. A divider is identified structurally *and* textually: some cell reads as
a domain heading, and no other cell in the row carries content that differs
from it. A question row always fails the second half, so a question whose text
mentions "Cloud Security" never opens a false section — which would otherwise
silently reassign every question below it. Merged heading rows (the usual case)
are expanded before detection. Numbering and punctuation variants are handled:
`1. Network Security`, `2) Application Security`, `Section 3 - Logical Access
Security`, `DATA SECURITY`, `5. Security Logging & Monitoring`.

Every question ends up in exactly one domain. Questions appearing before the
first heading are attributed to the first section and the section is marked
*inferred* so the preview highlights it for checking — they are never dropped.

Also handled: merged cells, blank and ragged rows, multi-line answers,
inconsistent header case and whitespace, UTF-8 BOMs, and comma / semicolon /
tab delimiters. Link Evidence is stored and displayed as text — the link is
never fetched (v1 non-goal); its presence or absence is passed to the AI as a
signal.

---

## Architecture

Strict dependency direction: `delivery → service → repository → database`.

**Every boundary is an interface declared at its own layer's root.** Repository
contracts live in `internal/repository`, service contracts in `internal/service`,
and the delivery contract in the handler package. Implementations depend on the
contracts, never on each other, so any of them can be swapped or stubbed without
touching the layers around it:

| Package | Contracts |
|---|---|
| `internal/repository` | one interface per model — `VendorRepository`, `AssessmentRepository`, `UploadRepository`, `QuestionRepository`, `ReviewResultRepository`, `AssessmentSummaryRepository`, `RubricRepository`, `AssessmentRubricRepository`, `ReviewJobRepository`, `UserRepository`, `RecoveryCodeRepository`, `SessionRepository` — plus `TxManager` |
| `internal/service` | `VendorService`, `IngestService`, `AssessmentService`, `SignOffService`, `RubricService`, `ReviewService`, `AuthService`, `QuestionnaireParser` |
| `internal/service/aiclient` | `AIReviewer` — declared alongside its own implementations (`anthropic`, `openAICompat`, `mock`), the same way each repository file declares its interface next to its struct |
| `delivery/http/handler/routes.go` | `Routes` — the delivery contract the router depends on instead of the concrete handler set |

The compile-time assertions proving each implementation satisfies its contract
sit in `routes.go`, the one place that knows both sides.

### Where the types live

| Package | Holds | Rule |
|---|---|---|
| `internal/model` | one struct per table, plus the enum types its columns are constrained to | data only, no behaviour; every field carries a `json` tag; ids are `uuid.UUID` |
| `internal/dto` | the shapes that cross a boundary but are not rows — `AssessmentFilter`, `QuestionFilter`, the ingestion preview (`Grid`, `SourceRow`, `Section`, `IngestPreview`), the AI request/response envelopes, `LoginResult`, `MFAEnrolment`, `BulkFinalizeResult`, `SignOffProgress` | may carry behaviour; a contract can name one without delivery importing an implementation |
| `internal/helper` | what more than one layer needs and no layer owns: the sentinel errors, `ValidationErrors`, the Postgres connection, risk banding, display labels, header normalisation | imports no repository, service or delivery package, so it can be imported anywhere |
| `internal/service` | the validation rules (`ValidateVendor`, `ValidateAssessment`, `ValidateQuestion`, `ValidateRubric`, `ValidateColumnMapping`) | validation is a rule about what may be written, not a property of the data |

`model` carrying no methods is the load-bearing part: read its file list and
you have read the schema. The behaviour that used to hang off those types —
`Validate`, `Band`, `Label`, `Percent` — moved to whichever of the three
packages above owns it, and the templates reach it through registered template
functions rather than calling methods on a row.

Primary keys are `uuid.UUID`, defaulted by Postgres with `gen_random_uuid()`.
The one exception is `sessions.id`, which stays an opaque random string: it is
a cookie token, and typing it as a uuid would invite treating a guessed uuid as
a valid session.

Nothing above `internal/repository/postgres` imports pgx, and nothing above
`internal/service/aiclient` knows which AI provider is configured.

### The component pattern

Every component in the repository, service and delivery layers is built the
same way, in four steps:

1. **Define the dependency interface** — what the component needs, named as
   interfaces and nothing concrete.
2. **Implement the concrete dependency** — the real thing that satisfies it.
3. **Implement the struct and its methods** — fields are the interfaces from
   step 1, held unexported.
4. **A constructor that ensures the dependency is injected** — it validates and
   returns an error, so incomplete wiring fails at startup naming the missing
   field.

| Layer | 1. Interface | 2. Concrete | 3. Struct | 4. Constructor |
|---|---|---|---|---|
| Repository | `postgres.ConnProvider`, `postgres.Querier` | `*postgres.DB` | `VendorRepo{db ConnProvider}` | `NewVendorRepo(ConnProvider)` |
| Service | `assessment.Deps` of `repository.*Repository` | the Postgres repositories | `Service{vendors, questions, …}` | `assessment.New(Deps) (*Service, error)` |
| Delivery | `handler.Deps` of `service.*Service` | the service structs | `Handler{assessments, reviews, auth}` | `handler.New(Deps) (*Handler, error)` |

Two consequences worth knowing:

**Services take the repositories they use, not the whole set.** `assessment.Deps`
lists vendors, domains, assessments, questions, results, summaries and rubrics —
so it is visible at a glance that ingestion does not touch jobs, users or
sessions. `FromRepositories` is the adapter the main package uses to build a
`Deps` from the full set.

**The constructors return an error rather than panicking.** A missing dependency
then fails at startup saying *which* one, instead of a nil-pointer panic on
whichever request reaches that repository first. `MustNew` exists for fixtures
that cannot meaningfully recover. `internal/service/assessment/deps_test.go`
removes each dependency in turn and asserts the error names it.

The `ConnProvider` indirection in the repository layer is what makes
transactions invisible to repository code: the provider returns the transaction
carried on the context when there is one and the pool otherwise, so no
repository method needs a transaction-aware variant.

```
cmd/server            main.go, wiring, graceful shutdown
internal/
  config              env loading and validation
  model               one struct per table; the ERD, in Go
  dto                 filters, ingestion preview, AI envelopes, service results
  helper              sentinel errors, DB connect, risk banding, labels
  repository          one repository interface per model
  repository/postgres  pgx implementations, migrations runner
  service              service contracts + validation rules
  service/
    parser            Excel/CSV reading, column mapping, domain detection
    assessment        vendors, assessments, ingestion
    aiclient          AIReviewer interface + openaicompat / anthropic / mock
    review            batching, scoring, aggregation, background worker
    auth              passwords, sessions, optional TOTP MFA
  delivery/http       chi router, handlers, middleware
web/                  html/template + Bootstrap 5 + vendored htmx (embedded)
  scss/               Sass source for the themed stylesheet
migrations/           golang-migrate SQL (embedded)
pkg/fuzzy             dependency-free string similarity
pkg/totp              RFC 6238 TOTP
```

`internal/delivery/http/router.go` is the security boundary: everything that
touches assessment data lives inside one authenticated group, and the only
routes outside it are the health check, static assets and the login flow.

Templates, static assets and migrations are embedded in the binary, so the
container image needs no volume and cannot serve a stale asset after a deploy.

### Styling

Bootstrap 5.3, compiled from Sass with the brand colour substituted, vendored
into `web/static/`. No CDN: the Content-Security-Policy is `script-src 'self'`,
so a CDN asset would silently fail to load.

The theme is compiled rather than overridden at runtime because overriding
`--bs-primary` alone does not retheme anything — Bootstrap compiles literal
values into `.btn-primary { --bs-btn-bg: ... }` and into every derived tint,
shade, focus ring and subtle background. `web/scss/tpsa.scss` sets `$primary`
and imports only the components this app uses (no modal, carousel, offcanvas,
popover or tooltip), which keeps the stylesheet about a third smaller than a
full build.

**The compiled CSS is committed**, so running the app still needs no build
step. After editing the Sass:

```bash
make css-deps    # once: installs bootstrap + sass locally
make css         # recompiles web/static/css/app.css
```

**Risk bands deliberately do not use Bootstrap's semantic colours.** With a red
brand colour, a "Critical" badge in Bootstrap's danger red would be
indistinguishable from a primary button, and the most important signal on the
page would read as decoration. The band scale runs green → amber → orange →
deep maroon, with critical darker and more saturated than the brand red so it
still reads as "worse" beside it. `$danger` is likewise kept a shade deeper
than `$primary` so a destructive action is not mistaken for the primary one.

Background reviews run in-process on a goroutine pool draining a `review_jobs`
table. The queue is a table rather than a broker because this is one team's
workload and it keeps the deployment to app + Postgres; `FOR UPDATE SKIP
LOCKED` still makes a second app instance safe. Jobs heartbeat while running,
and a job orphaned by a restart is re-queued instead of leaving an assessment
stuck in `reviewing` forever.

---

## Testing

```bash
make test                                  # unit tests only
export TEST_DATABASE_URL="postgres://postgres@localhost:5432/tpsa_test?sslmode=disable"
make test                                  # adds the database integration suite
make test-race
```

The integration suite skips itself when `TEST_DATABASE_URL` is unset, so
`go test ./...` passes on a machine with no database. It runs the real
migrations and walks the whole pipeline — upload, preview, mapping, review,
aggregation — using the production mock reviewer, so it exercises exactly the
code path a developer gets with `AI_PROVIDER=mock`.

Beyond the happy path, the suite pins the behaviours that are expensive to get
wrong:

- signing off never destroys the AI draft, and records who signed and when;
- a re-review preserves history and does not reopen signed-off work;
- bulk sign-off skips questions with no draft rather than signing an empty one;
- a closed assessment refuses every edit;
- the export labels every unsigned row as an unconfirmed draft;
- a failed batch falls back to per-question calls, and dropped questions are
  retried individually;
- re-mapping clears stale results rather than orphaning them;
- a job is claimed exactly once and a stalled job is reclaimed;
- an unknown username and a wrong password fail identically;
- a session pending MFA authorises nothing, and expires in minutes;
- a recovery code works once and only once;
- bootstrap creates the first account and never a second;
- deleting a vendor leaves no orphans.

---

## Known limits and open decisions

- **No PDF export.** CSV only. A PDF would need a layout engine and the CSV
  opens in the tools this team already uses.
- **No account administration UI.** Accounts are created by the first-run
  screen or `BOOTSTRAP_*`. Adding colleagues currently means an `INSERT`, or
  temporarily setting the bootstrap variables against an empty database. A
  small admin page is the obvious next increment.
- **Losing a phone with no recovery codes left** requires someone with database
  access to clear `mfa_enabled` on the row. With one internal team and no admin
  UI, a self-service reset would be a bigger hole than the problem it solves.
- **No login rate limiting.** bcrypt at cost 12 makes online guessing slow, but
  a per-IP limiter is worth adding before this is reachable from anywhere but
  an internal network.
- **Re-review scope** — a re-run reviews every question. Reviewing only
  unsigned questions would be cheaper; it was left alone because a changed
  rubric should re-examine everything.
- **Sub-batching** — batch size is per domain. A domain with more questions
  than `AI_BATCH_SIZE` is split, which slightly weakens contradiction detection
  within that domain.
