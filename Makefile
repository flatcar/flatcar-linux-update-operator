.PHONY:	all release-bin image clean test vendor
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
	go build -o $@ -ldflags $(LD_FLAGS) $(REPO)/cmd/$*

release-bin:
	./build/build-release.sh

test:
	go test -v $(REPO)/pkg/...

image:
	@$(DOCKER_CMD) build --rm=true -t $(IMAGE_REPO):$(VERSION) .

image-push: image
	@$(DOCKER_CMD) push $(IMAGE_REPO):$(VERSION)

vendor:
	glide update --strip-vendor
	glide-vc --use-lock-file --no-tests --only-code

clean:
	rm -rf bin
