FROM golang:1.13-alpine3.12 as builder

MAINTAINER Kinvolk

RUN apk add -U make git

WORKDIR /go/src/github.com/kinvolk/flatcar-linux-update-operator

COPY . .

RUN make bin/update-agent bin/update-operator

FROM alpine:3.12

MAINTAINER Kinvolk

RUN apk add -U ca-certificates
COPY --from=builder /go/src/github.com/kinvolk/flatcar-linux-update-operator/bin/update-agent /bin/
COPY --from=builder /go/src/github.com/kinvolk/flatcar-linux-update-operator/bin/update-operator /bin/

USER 65534:65534

ENTRYPOINT ["/bin/update-agent"]
