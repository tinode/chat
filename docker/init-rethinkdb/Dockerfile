# Docker file builds an image with a utility which initializes database storage.
# It needs to be run only once per database. Use it as (assuming your running RethinkDB
# container is named 'rethinkdb'):
# $ docker run --name=init-tinode-db --link rethinkdb --env UID_ENCRYPTION_KEY=base64+encoded+16+bytes init-rethinkdb

FROM golang:latest

MAINTAINER Gene Sokolov <gene@tinode.co>

# Compiles for RethinkDB by default. Alternatively use `--env TARGET_DB=mysql` to build for MySQL.
ENV TARGET_DB rethinkdb

# Build the utility.
RUN go get -tags $TARGET_DB github.com/tinode/chat/tinode-db \
	&& go install -tags $TARGET_DB github.com/tinode/chat/tinode-db

# Key to initialize UID generator. Replace with your own
# for production
ENV UID_ENCRYPTION_KEY la6YsO+bNX/+XIkOqc5Svw==

COPY config.template /
COPY entrypoint.sh /
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
