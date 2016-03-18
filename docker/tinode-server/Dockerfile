# Docker file builds an image with a tinode server and tinode-db initializer.
# The server exposes port 18080.
# In order to run the image you have to link it to a running RethinkDB container
# (assuming it's named 'rethinkdb') and map the port where the tinode server accepts connections:
#
# $ docker run -p 18080:6060 -d --link rethinkdb \
#	--env UID_ENCRYPTION_KEY=base64+encoded+16+bytes= \
#	--env API_KEY_SALT=base64+encoded+32+bytes \
#	--env AUTH_TOKEN_KEY=base64+encoded+32+bytes \
#	tinode-server

FROM golang

MAINTAINER Gene Sokolov <or.else@gmail.com>

RUN go get github.com/tinode/chat/server \
	&& go install github.com/tinode/chat/server

# Various encryption and salt keys. Replace with your own in production.

# Key to initialize UID generator
ENV UID_ENCRYPTION_KEY la6YsO+bNX/+XIkOqc5Svw==

# Key to sign API app id
ENV API_KEY_SALT T713/rYYgW7g4m3vG6zGRh7+FM1t0T8j13koXScOAj4=

#Key used to sign authentication tokens.
ENV AUTH_TOKEN_KEY wfaY2RgF2S1OQI/ZlK+LSrp1KB2jwAdGAIHQ7JZn+Kc=

COPY config.template /
COPY entrypoint.sh /
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]

EXPOSE 18080
