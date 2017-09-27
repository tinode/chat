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

FROM golang

MAINTAINER Gene Sokolov <gene@tinode.co>

RUN go get github.com/tinode/chat/server \
	&& go install -tags rethinkdb github.com/tinode/chat/server

# Download the demo app and the js bindings
RUN git clone --depth 1 https://github.com/tinode/example-react-js.git
RUN git clone --depth 1 https://github.com/tinode/tinode-js.git

# The demo app expects tinode.js to be in the same dir as the app, so create the link
RUN ln tinode-js/tinode.js example-react-js/

# Various encryption and salt keys. Replace with your own in production.

# Key to initialize UID generator
ENV UID_ENCRYPTION_KEY la6YsO+bNX/+XIkOqc5Svw==

# Key to sign API app ID.
ENV API_KEY_SALT T713/rYYgW7g4m3vG6zGRh7+FM1t0T8j13koXScOAj4=

#Key used to sign authentication tokens.
ENV AUTH_TOKEN_KEY wfaY2RgF2S1OQI/ZlK+LSrp1KB2jwAdGAIHQ7JZn+Kc=

# Copy config template to the container.
COPY config.template /
COPY entrypoint.sh /
RUN chmod +x /entrypoint.sh

# Generate config from template and run the server.
ENTRYPOINT ["/entrypoint.sh"]

EXPOSE 18080
