FROM docker.io/golang:1.26.4-alpine3.24 as builder

RUN apk add -U make git

WORKDIR /usr/src/github.com/flatcar/flatcar-linux-update-operator

COPY . .

RUN make bin/update-agent bin/update-operator

FROM docker.io/alpine:3.24

MAINTAINER Flatcar Maintainers

LABEL org.opencontainers.image.source https://github.com/flatcar/flatcar-linux-update-operator

RUN apk add -U ca-certificates

WORKDIR /bin

COPY --from=builder /usr/src/github.com/flatcar/flatcar-linux-update-operator/bin/update-agent .
COPY --from=builder /usr/src/github.com/flatcar/flatcar-linux-update-operator/bin/update-operator .

USER 65534:65534

ENTRYPOINT ["/bin/update-agent"]
