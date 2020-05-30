FROM alpine:3.11

ARG VERSION=0.16.4
ENV VERSION=$VERSION

LABEL maintainer="MidnightChat Team <info@MidnightChat.co>"
LABEL name="MidnightChatMetricExporter"
LABEL version=$VERSION

ENV SERVE_FOR=""
ENV WAIT_FOR=""

ENV MidnightChat_ADDR=http://localhost/stats/expvar/
ENV INSTANCE="exporter-instance"

ENV INFLUXDB_VERSION=1.7
ENV INFLUXDB_ORGANIZATION="org"
ENV INFLUXDB_PUSH_INTERVAL=60
ENV INFLUXDB_PUSH_ADDRESS=""
ENV INFLUXDB_AUTH_TOKEN=""

ENV PROM_NAMESPACE="MidnightChat"
ENV PROM_METRICS_PATH="/metrics"

WORKDIR /opt/MidnightChat

RUN apk add --no-cache bash

# Fetch exporter build from Github.
ADD https://github.com/MidnightChat/chat/releases/download/v$VERSION/exporter.linux-amd64 ./exporter

COPY entrypoint.sh .
RUN chmod +x exporter && chmod +x entrypoint.sh

ENTRYPOINT ./entrypoint.sh

EXPOSE 6222
