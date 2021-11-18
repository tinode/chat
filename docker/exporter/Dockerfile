FROM alpine:3.14

ARG VERSION=0.16.4
ENV VERSION=$VERSION

LABEL maintainer="Tinode Team <info@tinode.co>"
LABEL name="TinodeMetricExporter"
LABEL version=$VERSION

ENV SERVE_FOR=""
ENV WAIT_FOR=""

ENV TINODE_ADDR=http://localhost/stats/expvar/
ENV INSTANCE="exporter-instance"

ENV INFLUXDB_VERSION=1.7
ENV INFLUXDB_ORGANIZATION="org"
ENV INFLUXDB_PUSH_INTERVAL=60
ENV INFLUXDB_PUSH_ADDRESS=""
ENV INFLUXDB_AUTH_TOKEN=""

ENV PROM_NAMESPACE="tinode"
ENV PROM_METRICS_PATH="/metrics"

WORKDIR /opt/tinode

RUN apk add --no-cache bash

# Fetch exporter build from Github.
ADD https://github.com/tinode/chat/releases/download/v$VERSION/exporter.linux-amd64 ./exporter

COPY entrypoint.sh .
RUN chmod +x exporter && chmod +x entrypoint.sh

ENTRYPOINT ./entrypoint.sh

EXPOSE 6222
