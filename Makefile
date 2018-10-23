.PHONY: all gocast test

all:
	$(MAKE) deps
	$(MAKE) gocast

deps:
	go get -u golang.org/x/lint/golint
	go get -u github.com/golang/dep/cmd/dep
	dep ensure

gocast:
	go build .

debug:
	dep ensure
	go build -race .

test:
	go test -v -race -short -failfast ./...

linux:
	GOOS=linux GOARCH=amd64 go build -o gocast_linux .
