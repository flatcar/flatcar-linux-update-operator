FROM alpine:3.10
RUN apk add --no-cache ca-certificates
COPY bin /bin/

USER nobody

ENTRYPOINT ["/bin/update-agent"]
