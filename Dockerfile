FROM alpine:3.8
ADD ./output/kubernetes-watchdog /usr/bin


CMD ["/usr/bin/kubernetes-watchdog"]
