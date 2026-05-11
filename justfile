# terraform-provider-garage — task runner.
#
# Run `just` for a recipe listing.

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

project_name     := "terraform-provider-garage"
bin_dir          := "build/bin"
coverage_out     := "coverage.out"
allowed_licenses := "Apache-2.0,MIT,BSD-2-Clause,BSD-3-Clause,ISC,MPL-2.0"

# Resolved once per `just` invocation; cheap.
version := `git describe --tags --always --dirty 2>/dev/null || echo "dev"`
commit  := `git rev-parse --short HEAD 2>/dev/null || echo "dev"`

# Default: list recipes
_default:
    @just --list --unsorted

# ─── Build ─────────────────────────────────────────────────────────

# Build the provider binary into build/bin/
[group('build')]
build:
    @mkdir -p {{bin_dir}}
    go build -ldflags "-X main.version={{version}} -X main.commit={{commit}}" -o {{bin_dir}}/{{project_name}} ./cmd/{{project_name}}
    @echo "✓ Provider binary built"

# go install for use with ~/.terraformrc dev_overrides
[group('build')]
install:
    go install -v ./cmd/{{project_name}}

# Regenerate docs/ via tfplugindocs (from tools/)
[group('build')]
generate:
    cd tools && go generate ./...

# Remove build artifacts
[group('build')]
clean:
    rm -rf {{bin_dir}}
    rm -f {{coverage_out}}
    go clean -cache
    find . -name "*.test" -delete
    @echo "✓ Build artifacts cleaned"

# ─── Test ──────────────────────────────────────────────────────────

# Run unit tests with race detector
[group('test')]
test:
    go test -v -race ./...

# Run unit tests for a specific package: just test-pkg ./internal/provider
[group('test')]
test-pkg pkg:
    go test -v -race {{pkg}}

# Run unit tests with coverage profile
[group('test')]
test-coverage:
    go test -v -race -coverprofile={{coverage_out}} ./...

# Run tests + open HTML coverage report
[group('test')]
test-report:
    go test -coverprofile={{coverage_out}} ./...
    go tool cover -html={{coverage_out}}

# Run acceptance tests against real Garage (TF_ACC=1, 120m timeout)
[group('test')]
testacc:
    TF_ACC=1 go test -v -cover -timeout 120m ./internal/provider/

# ─── Lint & format ─────────────────────────────────────────────────

# Run golangci-lint
[group('lint')]
lint:
    golangci-lint run ./...

# Run golangci-lint with auto-fix
[group('lint')]
lint-fix:
    golangci-lint run --fix ./...

# Format code with gofmt + goimports
[group('lint')]
fmt:
    gofmt -s -w .
    goimports -w -local github.com/donaldgifford .

# ─── License compliance ────────────────────────────────────────────

# Check dependency licenses against the allow list
[group('license')]
license-check:
    go-licenses check ./... --allowed_licenses={{allowed_licenses}}

# Generate CSV report of all dependency licenses
[group('license')]
license-report:
    go-licenses report ./... --template=.github/licenses-csv.tpl

# ─── Release ───────────────────────────────────────────────────────

# Validate the goreleaser config
[group('release')]
release-check:
    goreleaser check

# Snapshot release locally (no publish, no sign)
[group('release')]
release-local:
    goreleaser release --snapshot --clean --skip=publish --skip=sign

# Tag and push a new release: just release v0.1.0
[group('release')]
release tag:
    git tag -a {{tag}} -m "Release {{tag}}"
    git push origin {{tag}}

# ─── Composite gates ───────────────────────────────────────────────

# Pre-commit gate: lint + test
[group('gate')]
check: lint test
    @echo "✓ Pre-commit checks passed"

# Full CI gate: lint + test + build + license-check
[group('gate')]
ci: lint test build license-check
    @echo "✓ CI pipeline complete"
