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

FROM golang:latest

LABEL maintainer="Gene Sokolov <gene@tinode.co>"
LABEL name="TinodeChatServer"
LABEL version="0.14.1"

# Compiles for RethinkDB by default. Alternatively use `--build-arg TARGET_DB=mysql` to build for MySQL.
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

# Build the database initialization utility.
RUN go get -tags $TARGET_DB github.com/tinode/chat/tinode-db \
	&& go install -tags $TARGET_DB github.com/tinode/chat/tinode-db

# Build the server binary from the master.
RUN go get -tags $TARGET_DB github.com/tinode/chat/server \
	&& go install -tags $TARGET_DB -ldflags "-X main.buildstamp=${TARGET_DB}-`date -u '+%Y%m%dT%H:%M:%SZ'`" \
		github.com/tinode/chat/server

# Download the demo app and the js bindings. Using wget to avoid problems with 'git clone' on Windows.
RUN wget -q -O example-react-js.tar.gz https://github.com/tinode/example-react-js/archive/master.tar.gz \
	&& mkdir example-react-js \
	&& tar -xzf example-react-js.tar.gz -C example-react-js/ --strip-components=1 \
	&& rm example-react-js.tar.gz
RUN wget -q -O tinode-js.tar.gz https://github.com/tinode/tinode-js/archive/master.tar.gz \
	&& mkdir tinode-js \
	&& tar -xzf tinode-js.tar.gz -C tinode-js/ --strip-components=1 \
	&& rm tinode-js.tar.gz
	
# The demo app expects tinode.js to be in the same dir as the app, so create the link.
RUN ln tinode-js/tinode.js example-react-js/

# Copy config template to the container.
COPY config.template /
COPY entrypoint.sh /

# Directory for chatbot data
RUN mkdir /botdata

# Make scripts runnable
RUN chmod +x /entrypoint.sh
RUN chmod +x /go/src/github.com/tinode/chat/tinode-db/credentials.sh

# Generate config from template and run the server.
ENTRYPOINT /entrypoint.sh

# HTTP and gRPC ports
EXPOSE 18080 16061

