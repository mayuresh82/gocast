FROM golang:1.12-alpine as builder

ENV GO111MODULE=on

RUN apk update && \
    apk upgrade && \
    apk add --no-cache git && \
    apk add make
RUN mkdir -p /go/src/github.com/mayuresh82/gocast

COPY . /go/src/github.com/mayuresh82/gocast

WORKDIR /go/src/github.com/mayuresh82/gocast

RUN go mod download
RUN make

FROM alpine:latest
RUN apk --no-cache add ca-certificates bash iptables netcat-openbsd sudo
WORKDIR /root/
COPY --from=builder /go/src/github.com/mayuresh82/gocast .

EXPOSE 8080/tcp

ENTRYPOINT ["./gocast"]
