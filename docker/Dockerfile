# syntax=docker/dockerfile:1

# ---- build ----------------------------------------------------------------
# Pinned to the exact toolchain in go.mod. A floating 1.25 tag that happened to
# be older would trigger a toolchain download mid-build, or fail outright if the
# builder has no network.
FROM golang:1.25.4-alpine AS build

WORKDIR /src

# Dependencies are copied first so a source-only change reuses the module
# cache layer instead of re-downloading everything.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# CGO is off so the result is a static binary the distroless image can run.
# Symbols and DWARF are stripped; there is no debugger in the runtime image.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/server ./cmd/server

# ---- runtime --------------------------------------------------------------
# Distroless: no shell, no package manager, nothing to exploit beyond the app
# itself. Templates, static assets and migrations are embedded in the binary,
# so this stage needs the binary and certificates only.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/server /server

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/server"]
