# build stage
FROM golang:1.11 AS build-env
WORKDIR /go/src/gitlab.com/frozy.io
ADD . connector/
WORKDIR connector
RUN make deps
RUN make build

# Broker itself
FROM alpine:3.8
RUN apk update && apk add ca-certificates
RUN addgroup -g 1000 frozyconnector && \
    adduser -D -u 1000 -G frozyconnector frozyconnector
USER frozyconnector
COPY --chown=frozyconnector:frozyconnector \
  --from=build-env \
  /go/src/gitlab.com/frozy.io/connector/connector \
  /home/frozyconnector/connector
ENV FROZY_CONFIG_DIR=/home/frozyconnector/.frozy-connector
ENTRYPOINT ["/home/frozyconnector/connector"]
