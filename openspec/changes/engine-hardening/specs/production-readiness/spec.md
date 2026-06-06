# Production Readiness Specification

## Purpose

Ensure the Engine service is deployable and observable in production: conservative GOMAXPROCS reserving CPU for FFmpeg, authenticated pprof for debugging, Testcontainers-based integration tests, and a CI pipeline enforcing lint/test/security gates.

## Requirements

### Requirement: GOMAXPROCS CPU Reservation

The system MUST set `GOMAXPROCS` to `max(1, runtime.NumCPU()-1)` at startup, reserving one core for FFmpeg subprocesses. This MUST execute before any goroutine pool is created.

#### Scenario: Multi-core container

- GIVEN the container has 4 vCPUs
- WHEN the Engine starts
- THEN `runtime.GOMAXPROCS(0)` returns 3

#### Scenario: Single-core container

- GIVEN the container has 1 vCPU
- WHEN the Engine starts
- THEN `runtime.GOMAXPROCS(0)` returns 1 (clamped, not 0)

### Requirement: Authenticated pprof Endpoint

The system MUST serve Go pprof endpoints (`/debug/pprof/`) on a separate admin port (default `:6060`). Every request MUST carry a valid PASETO admin token in the `Authorization: Bearer <token>` header. Requests without a valid token MUST receive HTTP 401.

#### Scenario: Authorized access

- GIVEN a valid PASETO admin token signed with the correct key
- WHEN a GET request hits `/debug/pprof/goroutine?debug=1` on the admin port with `Authorization: Bearer <token>`
- THEN the server returns HTTP 200 with the goroutine dump

#### Scenario: Missing token

- GIVEN no Authorization header
- WHEN any pprof endpoint is requested
- THEN the server returns HTTP 401 with body `{"error":"missing admin token"}`

#### Scenario: Invalid token

- GIVEN an expired or incorrectly-signed PASETO token
- WHEN any pprof endpoint is requested
- THEN the server returns HTTP 401 with body `{"error":"invalid admin token"}`

### Requirement: Testcontainers Integration Suite

The system MUST provide `TestMain`-based integration suites that spin up real Docker containers for DragonflyDB, NATS, and Weaviate. Each package's suite SHALL start one container, share it across all tests, and terminate it when the suite ends. Tests MUST skip via `testing.Short()` when Docker is unavailable.

#### Scenario: DragonflyDB integration tests

- GIVEN Docker is available
- WHEN `go test ./internal/cache/` runs
- THEN TestMain starts a `dragonflydb/dragonfly:v1.38.0` container
- AND all cache integration tests pass against the real instance

#### Scenario: Docker unavailable

- GIVEN Docker socket is not accessible
- WHEN `go test -short ./internal/cache/` runs
- THEN integration tests are skipped
- AND unit tests still execute and pass

### Requirement: Woodpecker CI Pipeline

The project MUST define a `.woodpecker.yml` pipeline with sequential stages: **lint** (golangci-lint v2.2) → **test** (`go test -race -count=1 ./...`) → **security** (govulncheck) → **sbom** (syft) → **build** (`CGO_ENABLED=0 go build ./cmd/engine`). The pipeline MUST fail on any non-zero exit code.

#### Scenario: Push triggers full pipeline

- GIVEN a push to any branch
- WHEN the Woodpecker agent picks up the pipeline
- THEN lint runs first; if it passes, test runs with `-race`
- THEN security and sbom run in any order
- THEN build runs last
- AND all stages must pass for the pipeline to be green

### Requirement: Linter Configuration

The project MUST define a `.golangci.yml` enabling: `govet`, `staticcheck`, `errcheck`, `gosec`, `goimports`, `ineffassign`, `misspell`. The config SHALL set `run.timeout: 5m` and `run.go: "1.26"`.

#### Scenario: Lint check fails CI

- GIVEN a commit introduces an unchecked error (errcheck violation)
- WHEN `golangci-lint run` executes in CI
- THEN the lint stage fails
- AND the test/security/build stages do not run
