FROM debian:7-slim

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

ADD ./output/kubernetes-watchdog /usr/bin


CMD ["/usr/bin/kubernetes-watchdog"]
