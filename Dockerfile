FROM alpine:3.8
RUN apk update && apk add openssh-client curl bash

RUN addgroup -g 1000 frozyconnector && \
    adduser -D -u 1000 -G frozyconnector frozyconnector
USER frozyconnector

COPY --chown=frozyconnector:frozyconnector \
  connector.sh docker_entrypoint.sh \
  /home/frozyconnector/

ENTRYPOINT [ "/home/frozyconnector/docker_entrypoint.sh" ]
