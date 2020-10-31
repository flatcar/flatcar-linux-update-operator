.PHONY:	all release-bin image clean test vendor ci lint-bin
export CGO_ENABLED:=0

VERSION=$(shell ./build/git-version.sh)
RELEASE_VERSION=$(shell cat VERSION)
COMMIT=$(shell git rev-parse HEAD)

REPO=github.com/kinvolk/flatcar-linux-update-operator
LD_FLAGS="-w -X $(REPO)/pkg/version.Version=$(RELEASE_VERSION) -X $(REPO)/pkg/version.Commit=$(COMMIT)"

DOCKER_CMD ?= docker
IMAGE_REPO?=quay.io/kinvolk/flatcar-linux-update-operator

all: bin/update-agent bin/update-operator

bin/%:
	go build -o $@ -ldflags $(LD_FLAGS) -mod=vendor $(REPO)/cmd/$*

release-bin:
	./build/build-release.sh

test:
	go test -mod=vendor -v $(REPO)/pkg/...

image:
	@$(DOCKER_CMD) build --rm=true -t $(IMAGE_REPO):$(VERSION) .

image-push: image
	@$(DOCKER_CMD) push $(IMAGE_REPO):$(VERSION)

vendor:
	go mod vendor

clean:
	rm -rf bin

ci: all test

lint-bin:
	@if [ "$$(git config --get diff.noprefix)" = "true" ]; then printf "\n\ngolangci-lint has a bug and can't run with the current git configuration: 'diff.noprefix' is set to 'true'. To override this setting for this repository, run the following command:\n\n'git config diff.noprefix false'\n\nFor more details, see https://github.com/golangci/golangci-lint/issues/948.\n\n\n"; exit 1; fi
	golangci-lint run --new-from-rev=$$(git merge-base $$(cat .git/resource/base_sha 2>/dev/null || echo "origin/master") HEAD) ./...
