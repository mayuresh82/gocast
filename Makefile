.PHONY: all gocast test

DOCKER_IMAGE := mayuresh82/gocast
VERSION := $(shell git describe --exact-match --tags 2>/dev/null)
BRANCH := $(shell git rev-parse --abbrev-ref HEAD)
COMMIT := $(shell git rev-parse --short HEAD)
DOCKER_TAG := $(COMMIT)
LDFLAGS := $(LDFLAGS) -X main.commit=$(COMMIT) -X main.branch=$(BRANCH)
ifdef VERSION
    LDFLAGS += -X main.version=$(VERSION)
	DOCKER_TAG = $(VERSION)
endif

all:
	$(MAKE) gocast

gocast:
	go build -ldflags "$(LDFLAGS)" .

debug:
	go build -race .

test:
	go test -v -race -short -failfast ./...

linux:
	GOOS=linux GOARCH=amd64 go build -o gocast_linux .
