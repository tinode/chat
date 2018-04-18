# Docker file builds an image with a tinode chat server.
# The server exposes port 18080.
# In order to run the image you have to link it to a running RethinkDB container
# (assuming it's named 'rethinkdb') and map the port where the tinode server accepts connections:
#
# $ docker run -p 6060:18080 -d --link rethinkdb \
#	--env UID_ENCRYPTION_KEY=base64+encoded+16+bytes= \
#	--env API_KEY_SALT=base64+encoded+32+bytes \
#	--env AUTH_TOKEN_KEY=base64+encoded+32+bytes \
#	tinode-server

FROM alpine:3.7

ARG VERSION=0.14.4
ENV VERSION=$VERSION

LABEL maintainer="Gene Sokolov <gene@tinode.co>"
LABEL name="TinodeChatServer"
LABEL version=$VERSION

# Builds for RethinkDB by default. Alternatively use `--build-arg TARGET_DB=mysql` to build for MySQL.
ARG TARGET_DB=rethinkdb
ENV TARGET_DB=$TARGET_DB

# Various encryption and salt keys. Replace with your own in production.

# Key to initialize UID generator
ENV UID_ENCRYPTION_KEY=la6YsO+bNX/+XIkOqc5Svw==

# Key to sign API app ID.
ENV API_KEY_SALT=T713/rYYgW7g4m3vG6zGRh7+FM1t0T8j13koXScOAj4=

# Key used to sign authentication tokens.
ENV AUTH_TOKEN_KEY=wfaY2RgF2S1OQI/ZlK+LSrp1KB2jwAdGAIHQ7JZn+Kc=

# Disable chatbot plugin by default. 
ENV PLUGIN_PYTHON_CHAT_BOT_ENABLED=false

# Adding bash and grep as they are used here.
RUN apk add --no-cache bash grep

WORKDIR /opt/tinode

ADD https://github.com/tinode/chat/releases/download/v$VERSION/tinode-$TARGET_DB.linux-amd64.tar.gz .

RUN tar -xzf tinode-$TARGET_DB.linux-amd64.tar.gz

# Copy config template to the container.
COPY config.template .
COPY entrypoint.sh .

# Directory for chatbot data
RUN mkdir /botdata

# Make scripts runnable
RUN chmod +x entrypoint.sh
RUN chmod +x credentials.sh

# Generate config from template and run the server.
ENTRYPOINT ./entrypoint.sh

# HTTP and gRPC ports
EXPOSE 18080 16061

