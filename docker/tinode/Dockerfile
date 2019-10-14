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

FROM alpine:latest

ARG VERSION=0.16.2
ENV VERSION=$VERSION

LABEL maintainer="Tinode Team <info@tinode.co>"
LABEL name="TinodeChatServer"
LABEL version=$VERSION

# Build-time options.

# Database selector. Builds for RethinkDB by default.
# Alternatively use `--build-arg TARGET_DB=mysql` to build for MySQL.
ARG TARGET_DB=rethinkdb
ENV TARGET_DB=$TARGET_DB

# Runtime options.

# An option to reset database.
ENV RESET_DB=false

# An option to upgrade database.
ENV UPGRADE_DB=false

# Load sample data to database from data.json.
ARG DEFAULT_SAMPLE_DATA=data.json
ENV SAMPLE_DATA=

# The MySQL DSN connection.
ENV MYSQL_DSN='root@tcp(mysql)/tinode'

# Disable chatbot plugin by default.
ENV PLUGIN_PYTHON_CHAT_BOT_ENABLED=false

# Default handler for large files
ENV MEDIA_HANDLER=fs

# Whitelisted domains for S3 large media handler.
ENV AWS_CORS_ORIGINS='["*"]'

# Default externally-visible hostname for email verification.
ENV SMTP_HOST_URL='http://localhost:6060'

# Whitelist of permitted email domains for email verification (empty list means all domains are permitted)
ENV SMTP_DOMAINS=''

# Various encryption and salt keys. Replace with your own in production.

# Salt used to generate the API key. Don't change it unless you also change the
# API key in the webapp & Android.
ENV API_KEY_SALT=T713/rYYgW7g4m3vG6zGRh7+FM1t0T8j13koXScOAj4=

# Key used to sign authentication tokens.
ENV AUTH_TOKEN_KEY=wfaY2RgF2S1OQI/ZlK+LSrp1KB2jwAdGAIHQ7JZn+Kc=

# Key to initialize UID generator
ENV UID_ENCRYPTION_KEY=la6YsO+bNX/+XIkOqc5Svw==

# Disable TLS by default.
ENV TLS_ENABLED=false

# Disable push notifications by default.
ENV FCM_PUSH_ENABLED=false

# Enable Android-specific notifications by default.
ENV FCM_INCLUDE_ANDROID_NOTIFICATION=true

# Install root certificates, they are needed for email validator to work
# with the TLS SMTP servers like Gmail. Also add bash and grep.
RUN apk update && \
	apk add --no-cache ca-certificates bash grep

WORKDIR /opt/tinode

# Get the desired Tinode build.
ADD https://github.com/tinode/chat/releases/download/v$VERSION/tinode-$TARGET_DB.linux-amd64.tar.gz .

# Unpack the Tinode archive.
RUN tar -xzf tinode-$TARGET_DB.linux-amd64.tar.gz \
	&& rm tinode-$TARGET_DB.linux-amd64.tar.gz

# Copy config template to the container.
COPY config.template .
COPY entrypoint.sh .

# Create directory for chatbot data.
RUN mkdir /botdata

# Make scripts runnable
RUN chmod +x entrypoint.sh
RUN chmod +x credentials.sh

# Generate config from template and run the server.
ENTRYPOINT ./entrypoint.sh

# HTTP and gRPC ports
EXPOSE 18080 16061
