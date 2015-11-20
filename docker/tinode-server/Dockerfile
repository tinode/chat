# Docker file builds an image with a tinode server and tinode-db initializer.
# The server exposes port 8080.
# In order to run the image you have to link it to a running RethinkDB container
# (assuming it's named 'rethinkdb') and map the port:
#
# $ docker run -p 8080:6060 -d --link rethinkdb --env SNOWFLAKE_UID_KEY=base64+encoded+24+bytes= tinode-server

FROM golang

MAINTAINER Gene Sokolov <or.else@gmail.com>

RUN go get github.com/tinode/chat/server \
	&& go install github.com/tinode/chat/server

# Default values for RethinkDB host and port.
# They will be overritten by linking to rethinkdb container
ENV RETHINKDB_PORT_28015_TCP_ADDR localhost
ENV RETHINKDB_PORT_28015_TCP_PORT 28015

# Key to initialize UID generator. Replace with your own
# for production
ENV SNOWFLAKE_UID_KEY la6YsO+bNX/+XIkOqc5Svw==

COPY config.template /
COPY entrypoint.sh /
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]

EXPOSE 8080
