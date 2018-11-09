FROM kroniak/ssh-client

RUN addgroup -g 1000 frozyconnector && \
    adduser -D -u 1000 -G frozyconnector frozyconnector
USER frozyconnector

COPY --chown=frozyconnector:frozyconnector connector.sh /home/frozyconnector/connector.sh

ENTRYPOINT [ "/home/frozyconnector/connector.sh" ]
