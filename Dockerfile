FROM golang:1.25-alpine AS builder
RUN apk update && \
    apk upgrade && \
    apk add --no-cache git && \
    apk add make

RUN mkdir -p /go/src/github.com/mayuresh82/gocast

COPY . /go/src/github.com/mayuresh82/gocast

WORKDIR /go/src/github.com/mayuresh82/gocast

RUN make linux

FROM alpine:latest
WORKDIR /root/

RUN apk --no-cache add ca-certificates bash iptables netcat-openbsd sudo

COPY --from=builder /go/src/github.com/mayuresh82/gocast/gocast /bin/

EXPOSE 8080/tcp

ENTRYPOINT ["/bin/gocast"]
