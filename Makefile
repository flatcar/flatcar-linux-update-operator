export CGO_ENABLED:=0

VERSION=$(shell ./build/git-version.sh)
RELEASE_VERSION=$(shell cat VERSION)
COMMIT=$(shell git rev-parse HEAD)

REPO=github.com/flatcar-linux/flatcar-linux-update-operator
LD_FLAGS="-w -X $(REPO)/pkg/version.Version=$(RELEASE_VERSION) -X $(REPO)/pkg/version.Commit=$(COMMIT)"

DOCKER_CMD ?= docker
IMAGE_REPO?=ghcr.io/flatcar-linux/flatcar-linux-update-operator

GOLANGCI_LINT_CONFIG_FILE ?= .golangci.yml

.PHONY: all
all: build test lint semgrep ## Compiles binaries, runs unit tests and runs linter.

bin/%: ## Builds individual binaries.
	go build -o $@ -ldflags $(LD_FLAGS) -mod=vendor ./cmd/$*

.PHONY: build
build: ## Builds all binaries.
	go build -ldflags $(LD_FLAGS) -mod=vendor ./cmd/...

.PHONY: build-test
build-test: ## Compiles unit tests.
	go test -run=nonexistent -mod=vendor -tags integration ./...

.PHONY: test
test: ## Runs unit tests.
	CGO_ENABLED= go test -mod=vendor -race ./...

.PHONY: generate
generate: ## Updates generated source files.
	go generate -mod=vendor -tags generate -v -x ./...

.PHONY: image
image: ## Builds FLUO Docker image.
	@$(DOCKER_CMD) build --rm=true -t $(IMAGE_REPO):$(VERSION) .

.PHONY: image-push
image-push: image ## Builds and pushes FLUO Docker image.
	@$(DOCKER_CMD) push $(IMAGE_REPO):$(VERSION)

.PHONY: vendor
vendor: ## Updates vendor directory.
	go mod vendor

.PHONY: clean
clean: ## Cleans build artifacts.
	rm -rf bin

.PHONY: ci
ci: check-generate check-vendor check-tidy build test test-integration ## Runs checks performed by CI without external dependencies required (e.g. golangci-lint).

.PHONY: check-working-tree-clean
check-working-tree-clean: ## Checks if working directory is clean.
	@test -z "$$(git status --porcelain)" || (echo "Commit all changes before running this target"; exit 1)

.PHONY: check-generate
check-generate: check-working-tree-clean generate ## Checks if generated source files are up to date.
	@test -z "$$(git status --porcelain)" || (echo "Please run 'make generate' and commit generated changes."; git --no-pager diff; exit 1)

.PHONY: check-vendor
check-vendor: check-working-tree-clean vendor ## Checks if vendor directory is up to date.
	@test -z "$$(git status --porcelain)" || (echo "Please run 'make vendor' and commit generated changes."; git status; exit 1)

.PHONY: check-tidy
check-tidy: check-working-tree-clean ## Checks if Go module files are clean.
	go mod tidy
	@test -z "$$(git status --porcelain)" || (echo "Please run 'go mod tidy' and commit generated changes."; git status; exit 1)

.PHONY: check-update-linters
check-update-linters: check-working-tree-clean update-linters ## Checks if list of enabled golangci-lint linters is up to date.
	@test -z "$$(git status --porcelain)" || (echo "Linter configuration outdated. Run 'make update-linters' and commit generated changes to fix."; exit 1)

.PHONY: update-linters
update-linters: ## Updates list of enabled golangci-lint linters.
	# Remove all enabled linters.
	sed -i '/^  enable:/q0' $(GOLANGCI_LINT_CONFIG_FILE)
	# Then add all possible linters to config.
	golangci-lint linters | grep -E '^\S+:' | cut -d: -f1 | sort | sed 's/^/    - /g' | grep -v -E "($$(sed -e '1,/^  disable:$$/d' .golangci.yml  | grep -E '    - \S+$$' | awk '{print $$2}' | tr \\n '|' | sed 's/|$$//g'))" >> $(GOLANGCI_LINT_CONFIG_FILE)

.PHONY: lint
lint: build build-test ## Runs golangci-lint.
	@if [ "$$(git config --get diff.noprefix)" = "true" ]; then printf "\n\ngolangci-lint has a bug and can't run with the current git configuration: 'diff.noprefix' is set to 'true'. To override this setting for this repository, run the following command:\n\n'git config diff.noprefix false'\n\nFor more details, see https://github.com/golangci/golangci-lint/issues/948.\n\n\n"; exit 1; fi
	golangci-lint run --new-from-rev=$$(git merge-base $$(cat .git/resource/base_sha 2>/dev/null || echo "origin/master") HEAD) ./...

.PHONY: codespell
codespell: CODESPELL_SKIP := $(shell cat .codespell.skip | tr \\n ',')
codespell: CODESPELL_BIN := codespell
codespell: ## Runs spell checking.
	which $(CODESPELL_BIN) >/dev/null 2>&1 || (echo "$(CODESPELL_BIN) binary not found, skipping spell checking"; exit 0)
	$(CODESPELL_BIN) --skip $(CODESPELL_SKIP) --ignore-words .codespell.ignorewords --check-filenames --check-hidden

.PHONY: test-up
test-up: ## Starts testing D-Bus instance in Docker container using docker-compose.
	env UID=$$(id -u) docker-compose -f test/docker-compose.yml up -d

.PHONY: test-down
test-down: ## Tears down testing D-Bus instance created by 'test-up'.
	env UID=$$(id -u) docker-compose -f test/docker-compose.yml down

.PHONY: test-integration
test-integration: test-up
test-integration: ## Runs integration tests using D-Bus running in Docker container.
	FLUO_TEST_DBUS_SOCKET=$$(realpath ./test/test_bus_socket) go test -mod=vendor -count 1 -tags integration ./...
	make test-down

.PHONY: install-changelog
install-changelog:
	go install github.com/rcmachado/changelog@0.7.0

.PHONY: format-changelog
format-changelog: ## Formats changelog using github.com/rcmachado/changelog.
	changelog fmt -o CHANGELOG.md.fmt
	mv CHANGELOG.md.fmt CHANGELOG.md

.PHONY: test-changelog
test-changelog: check-working-tree-clean ## Verifies that changelog is properly formatted.
	make format-changelog
	@test -z "$$(git status --porcelain)" || (echo "Please run 'make format-changelog' and commit generated changes."; git diff; exit 1)

.PHONY: build-kustomize
build-kustomize: ## Renders manifests using kustomize.
	kustomize build examples/deploy/

.PHONY: semgrep
semgrep: SEMGREP_BIN=semgrep
semgrep: ## Runs semgrep linter.
	@if ! which $(SEMGREP_BIN) >/dev/null 2>&1; then echo "$(SEMGREP_BIN) binary not found, skipping extra linting"; else $(SEMGREP_BIN); fi

.PHONY: help
help: ## Prints help message.
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
