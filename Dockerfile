# build stage
FROM golang:1.12beta2 AS build-env
ENV GO111MODULE=on
ARG GOPROXY
ENV GOPROXY=$GOPROXY
WORKDIR /go/src/gitlab.com/frozy.io
ADD . connector/
WORKDIR connector
ARG VERSION
RUN make deps
RUN VERSION=$VERSION make build

# Broker itself
FROM alpine:3.8
RUN apk update && apk add ca-certificates
RUN addgroup -g 1000 frozy && \
    adduser -D -u 1000 -G frozy frozy
USER frozy
COPY --chown=frozy:frozy \
  --from=build-env \
  /go/src/gitlab.com/frozy.io/connector/bin/connector-linux-amd64* \
  /home/frozy/connector
ENV FROZY_CONFIG_DIR=/home/frozy/.frozy-connector
RUN mkdir -p $FROZY_CONFIG_DIR
ENTRYPOINT ["/home/frozy/connector"]
