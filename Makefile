.PHONY:	all build build-test generate image clean test vendor ci lint-bin codespell
export CGO_ENABLED:=0

VERSION=$(shell ./build/git-version.sh)
RELEASE_VERSION=$(shell cat VERSION)
COMMIT=$(shell git rev-parse HEAD)

REPO=github.com/kinvolk/flatcar-linux-update-operator
LD_FLAGS="-w -X $(REPO)/pkg/version.Version=$(RELEASE_VERSION) -X $(REPO)/pkg/version.Commit=$(COMMIT)"

DOCKER_CMD ?= docker
IMAGE_REPO?=quay.io/kinvolk/flatcar-linux-update-operator

all: build test lint-bin

bin/%:
	go build -o $@ -ldflags $(LD_FLAGS) -mod=vendor ./cmd/$*

build:
	go build -ldflags $(LD_FLAGS) -mod=vendor ./cmd/...

build-test:
	go test -run=nonexistent -mod=vendor ./...

test:
	go test -mod=vendor -v ./...

generate:
	go generate -mod=vendor -v -x ./...

image:
	@$(DOCKER_CMD) build --rm=true -t $(IMAGE_REPO):$(VERSION) .

image-push: image
	@$(DOCKER_CMD) push $(IMAGE_REPO):$(VERSION)

vendor:
	go mod vendor

clean:
	rm -rf bin

ci: build test

lint-bin: build build-test
	@if [ "$$(git config --get diff.noprefix)" = "true" ]; then printf "\n\ngolangci-lint has a bug and can't run with the current git configuration: 'diff.noprefix' is set to 'true'. To override this setting for this repository, run the following command:\n\n'git config diff.noprefix false'\n\nFor more details, see https://github.com/golangci/golangci-lint/issues/948.\n\n\n"; exit 1; fi
	golangci-lint run --new-from-rev=$$(git merge-base $$(cat .git/resource/base_sha 2>/dev/null || echo "origin/master") HEAD) ./...

codespell: CODESPELL_SKIP := $(shell cat .codespell.skip | tr \\n ',')
codespell: CODESPELL_BIN := codespell
codespell:
	which $(CODESPELL_BIN) >/dev/null 2>&1 || (echo "$(CODESPELL_BIN) binary not found, skipping spell checking"; exit 0)
	$(CODESPELL_BIN) --skip $(CODESPELL_SKIP) --ignore-words .codespell.ignorewords --check-filenames --check-hidden
