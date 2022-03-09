FROM golang:1.14-alpine as builder
RUN apk update && \
    apk upgrade && \
    apk add --no-cache git && \
    apk add make

RUN mkdir -p /go/src/github.com/mayuresh82/gocast

COPY . /go/src/github.com/mayuresh82/gocast

WORKDIR /go/src/github.com/mayuresh82/gocast

RUN make linux

FROM alpine:latest
RUN apk --no-cache add ca-certificates bash iptables netcat-openbsd sudo
WORKDIR /root/
COPY --from=builder /go/src/github.com/mayuresh82/gocast .

EXPOSE 8080/tcp

ENTRYPOINT ["./gocast"]
