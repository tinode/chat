FROM alpine:latest

ARG VERSION=0.16.4
ENV VERSION=$VERSION

ENV SERVE_FOR=""

ENV TINODE_ADDR=http://localhost/stats/expvar/
ENV INSTANCE="exporter-instance"
ENV LISTEN_AT=":6222"

ENV INFLUXDB_VERSION=1.7
ENV INFLUXDB_ORGANIZATION="org"
ENV INFLUXDB_PUSH_INTERVAL=60
ENV INFLUXDB_PUSH_ADDRESS=http://localhost:6222/write
ENV INFLUXDB_AUTH_TOKEN="Your-token"

ENV PROM_NAMESPACE="tinode"
ENV PROM_METRICS_PATH="/metrics"

LABEL maintainer="Tinode Team <info@tinode.co>"
LABEL name="TinodeMetricExporter"
LABEL version=$VERSION

WORKDIR /opt/tinode

RUN apk add --no-cache bash

# Fetch exporter build from Github.
ADD https://github.com/tinode/chat/releases/download/v$VERSION/exporter.linux-amd64 ./exporter

COPY entrypoint.sh .
RUN chmod +x exporter && chmod +x entrypoint.sh

ENTRYPOINT ./entrypoint.sh

EXPOSE 6222
