FROM python:3.12-alpine

RUN addgroup -S signer && adduser -S -G signer -h /nonexistent signer
COPY statsig-signer /usr/local/bin/statsig-signer
RUN chmod 0555 /usr/local/bin/statsig-signer

USER signer
EXPOSE 8788
ENTRYPOINT ["/usr/local/bin/statsig-signer"]
