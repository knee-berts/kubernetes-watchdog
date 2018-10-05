FROM alpine:3.8

RUN apk --no-cache add ca-certificates

ADD ./output/kubernetes-watchdog /usr/bin


CMD ["/usr/bin/kubernetes-watchdog"]
