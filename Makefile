.PHONY: all gocast test

all:
	$(MAKE) gocast

gocast:
	go build -mod=vendor .

debug:
	go build -mod=vendor -race .

test:
	go test -v -race -short -failfast -mod=vendor ./...

linux:
	GOOS=linux GOARCH=amd64 go build -o gocast_linux -mod=vendor .
