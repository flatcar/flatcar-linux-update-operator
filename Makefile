export CGO_ENABLED:=0

VERSION=$(shell ./build/git-version.sh)
RELEASE_VERSION=$(shell cat VERSION)
COMMIT=$(shell git rev-parse HEAD)

REPO=github.com/kinvolk/flatcar-linux-update-operator
LD_FLAGS="-w -X $(REPO)/pkg/version.Version=$(RELEASE_VERSION) -X $(REPO)/pkg/version.Commit=$(COMMIT)"

DOCKER_CMD ?= docker
IMAGE_REPO?=quay.io/kinvolk/flatcar-linux-update-operator

.PHONY: all
all: build test lint

bin/%:
	go build -o $@ -ldflags $(LD_FLAGS) -mod=vendor ./cmd/$*

.PHONY: build
build:
	go build -ldflags $(LD_FLAGS) -mod=vendor ./cmd/...

.PHONY: build-test
build-test:
	go test -run=nonexistent -mod=vendor ./...

.PHONY: test
test:
	go test -mod=vendor -v ./...

.PHONY: generate
generate:
	go generate -mod=vendor -v -x ./...

.PHONY: image
image:
	@$(DOCKER_CMD) build --rm=true -t $(IMAGE_REPO):$(VERSION) .

.PHONY: image-push
image-push: image
	@$(DOCKER_CMD) push $(IMAGE_REPO):$(VERSION)

.PHONY: vendor
vendor:
	go mod vendor

.PHONY: clean
clean:
	rm -rf bin

.PHONY: ci
ci: check-generate check-vendor build test

.PHONY: check-working-tree-clean
check-working-tree-clean:
	@test -z "$$(git status --porcelain)" || (echo "Commit all changes before running this target"; exit 1)

.PHONY: check-generate
check-generate: check-working-tree-clean generate
	@test -z "$$(git status --porcelain)" || (echo "Please run 'make generate' and commit generated changes."; git --no-pager diff; exit 1)

.PHONY: check-vendor
check-vendor: check-working-tree-clean vendor
	@test -z "$$(git status --porcelain)" || (echo "Please run 'make vendor' and commit generated changes."; git status; exit 1)

.PHONY: check-tidy
check-tidy: check-working-tree-clean
	go mod tidy
	@test -z "$$(git status --porcelain)" || (echo "Please run 'go mod tidy' and commit generated changes."; git status; exit 1)

.PHONY: lint
lint: build build-test
	@if [ "$$(git config --get diff.noprefix)" = "true" ]; then printf "\n\ngolangci-lint has a bug and can't run with the current git configuration: 'diff.noprefix' is set to 'true'. To override this setting for this repository, run the following command:\n\n'git config diff.noprefix false'\n\nFor more details, see https://github.com/golangci/golangci-lint/issues/948.\n\n\n"; exit 1; fi
	golangci-lint run --new-from-rev=$$(git merge-base $$(cat .git/resource/base_sha 2>/dev/null || echo "origin/master") HEAD) ./...

.PHONY: codespell
codespell: CODESPELL_SKIP := $(shell cat .codespell.skip | tr \\n ',')
codespell: CODESPELL_BIN := codespell
codespell:
	which $(CODESPELL_BIN) >/dev/null 2>&1 || (echo "$(CODESPELL_BIN) binary not found, skipping spell checking"; exit 0)
	$(CODESPELL_BIN) --skip $(CODESPELL_SKIP) --ignore-words .codespell.ignorewords --check-filenames --check-hidden
