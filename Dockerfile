FROM golang:1.13-alpine3.10 as builder
MAINTAINER Kinvolk

RUN apk add -U make git

WORKDIR /go/src/github.com/kinvolk/flatcar-linux-update-operator

COPY . .

RUN make all

FROM alpine:3.10
MAINTAINER Kinvolk

RUN apk add -U ca-certificates
COPY --from=builder /go/src/github.com/kinvolk/flatcar-linux-update-operator/bin/update-agent /bin/
COPY --from=builder /go/src/github.com/kinvolk/flatcar-linux-update-operator/bin/update-operator /bin/

USER nobody

ENTRYPOINT ["/bin/update-agent"]
