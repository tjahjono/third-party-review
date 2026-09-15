# TPSA Reviewer

# --env-file is explicit because Compose resolves a bare `.env` relative to the
# compose file's own directory (docker/), not the repository root. Without it,
# the .env you create at the root is silently ignored and every ${VAR} falls
# back to its default. Run these targets from the repository root.
COMPOSE := docker compose -f docker/docker-compose.yml --env-file .env

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## Run the app locally (needs DATABASE_URL and a running Postgres)
	go run ./cmd/server

.PHONY: build
build: ## Build the server binary
	go build -trimpath -o bin/server ./cmd/server

.PHONY: test
test: ## Run the test suite
	go test ./... -count=1

.PHONY: test-race
test-race: ## Run the test suite with the race detector
	go test ./... -race -count=1

.PHONY: cover
cover: ## Run tests and open a coverage summary
	go test ./... -coverprofile=coverage.out -count=1
	go tool cover -func=coverage.out | tail -20

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format the code
	gofmt -w ./cmd ./internal ./pkg

.PHONY: css
css: ## Recompile the themed Bootstrap stylesheet (needs npx; output is committed)
	npx --yes sass@1 --load-path=node_modules --style=compressed --no-source-map \
	  web/scss/tpsa.scss web/static/css/app.css
	@echo "Rebuilt web/static/css/app.css - commit it; the app needs no build step."

.PHONY: css-deps
css-deps: ## Install the Sass toolchain needed by 'make css'
	npm install --no-save bootstrap@5.3 sass@1

.PHONY: check
check: fmt vet test ## Format, vet and test

.PHONY: env
env: ## Create .env from .env.example if it does not exist
	@test -f .env && echo ".env already exists" || { \
		cp .env.example .env; \
		echo "Created .env from .env.example."; \
		echo "Set SESSION_SECRET before starting:  openssl rand -hex 32"; \
	}

.PHONY: check-env
check-env:
	@test -f .env || { \
		echo "No .env found. Run 'make env', set SESSION_SECRET, then try again."; \
		exit 1; \
	}

.PHONY: up
up: check-env ## Start app + postgres in the background
	$(COMPOSE) up -d --build
	@echo
	@echo "TPSA Reviewer is starting on http://localhost:$${APP_PORT:-8080}"
	@echo "Follow the logs with 'make logs'."

.PHONY: down
down: ## Stop the stack (data volume is kept)
	$(COMPOSE) down

.PHONY: reset
reset: ## Stop the stack and delete the database volume
	$(COMPOSE) down -v

.PHONY: logs
logs: ## Follow application logs
	$(COMPOSE) logs -f app

.PHONY: psql
psql: ## Open a psql shell against the compose database
	$(COMPOSE) exec postgres psql -U $${POSTGRES_USER:-tpsa} -d $${POSTGRES_DB:-tpsa}

.PHONY: migrate-down
migrate-down: ## Roll the schema back by one migration (destructive)
	@echo "Migrations run automatically on startup. To roll back, use golang-migrate directly:"
	@echo '  migrate -path migrations -database "$$DATABASE_URL" down 1'
