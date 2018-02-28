# Docker file builds an image with a utility which initializes database storage.
# It needs to be run only once per database. Use it as (assuming your DB is RethinkDB and the database
# container is named 'rethinkdb'):
# $ docker run --name=init-tinode-db --link rethinkdb --env UID_ENCRYPTION_KEY=base64+encoded+16+bytes init-db

FROM golang:latest

LABEL maintainer="Gene Sokolov <gene@tinode.co>"
LABEL name="TinodeDBInitializer"
LABEL version="0.14.1"

# Compiles for RethinkDB by default. Alternatively use `--build-arg TARGET_DB=mysql` to build for MySQL.
ARG TARGET_DB=rethinkdb
ENV TARGET_DB=$TARGET_DB

# Key to initialize UID generator. Replace with your own
# for production
ENV UID_ENCRYPTION_KEY=la6YsO+bNX/+XIkOqc5Svw==

# Build the utility from the master branch on Github.
RUN go get -tags $TARGET_DB github.com/tinode/chat/tinode-db \
	&& go install -tags $TARGET_DB github.com/tinode/chat/tinode-db

COPY config.template /
COPY entrypoint.sh /
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
