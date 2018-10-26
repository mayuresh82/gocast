FROM golang:alpine as builder
RUN apk update && \
    apk upgrade && \
    apk add --no-cache git && \
    apk add make
RUN mkdir -p /opt/gocast
RUN mkdir -p /go/src/github.com/mayuresh82
RUN cd /go/src/github.com/mayuresh82 && \
    git clone https://github.com/mayuresh82/gocast
WORKDIR /go/src/github.com/mayuresh82/gocast
RUN make
RUN cp gocast /opt/gocast/

FROM alpine:latest
RUN apk --no-cache add ca-certificates bash iptables netcat-openbsd sudo
WORKDIR /root/
COPY --from=builder /opt/gocast/gocast .

EXPOSE 8080/tcp

ENTRYPOINT ["./gocast"]
