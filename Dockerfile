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
RUN addgroup -g 1000 frozyconnector && \
    adduser -D -u 1000 -G frozyconnector frozyconnector
USER frozyconnector
COPY --chown=frozyconnector:frozyconnector \
  --from=build-env \
  /go/src/gitlab.com/frozy.io/connector/bin/connector-linux-amd64* \
  /home/frozyconnector/connector
ENV FROZY_CONFIG_DIR=/home/frozyconnector/.frozy-connector
ENTRYPOINT ["/home/frozyconnector/connector"]
