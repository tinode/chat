# Docker file builds an image with a tinode chat server.
#
# In order to run the image you have to link it to a running database container. For example, to
# to use RethinkDB (named 'rethinkdb') and map the port where the tinode server accepts connections:
#
# $ docker run -p 6060:6060 -d --link rethinkdb \
#	--env UID_ENCRYPTION_KEY=base64+encoded+16+bytes= \
#	--env API_KEY_SALT=base64+encoded+32+bytes \
#	--env AUTH_TOKEN_KEY=base64+encoded+32+bytes \
#	tinode-server

FROM alpine:3.17

ARG VERSION=0.21
ENV VERSION=$VERSION
ARG BINVERS=$VERSION

LABEL maintainer="Tinode Team <info@tinode.co>"
LABEL name="TinodeChatServer"
LABEL version=$VERSION

# Build-time options.

# Database selector. Builds for MySQL by default.
# Alternatively use one of: postgres mongodb rethinkdb for a corresponsing
# DB backend or alldbs to build a generic Tinode docker image, for example:
# `--build-arg TARGET_DB=postgres` to build for PostgreSQL.
ARG TARGET_DB=mysql
ENV TARGET_DB=$TARGET_DB

# Runtime options.

# Specifies the database host:port pair to wait for before running Tinode.
# Ignored if empty.
ENV WAIT_FOR=

# An option to reset database.
ENV RESET_DB=false

# An option to upgrade database.
ENV UPGRADE_DB=false

# Option to skip DB initialization when it's missing.
ENV NO_DB_INIT=false

# Load sample data to database from data.json.
ARG SAMPLE_DATA=data.json
ENV SAMPLE_DATA=$SAMPLE_DATA

# The MySQL DSN connection.
ENV MYSQL_DSN='root@tcp(mysql)/tinode'

# The PostgreSQL DSN connection.
ENV POSTGRES_DSN='postgresql://postgres:postgres@localhost:5432/tinode'

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
# Declare FCM-related vars
ENV FCM_API_KEY=
ENV FCM_APP_ID=
ENV FCM_SENDER_ID=
ENV FCM_PROJECT_ID=
ENV FCM_VAPID_KEY=
ENV FCM_MEASUREMENT_ID=

# Enable Android-specific notifications by default.
ENV FCM_INCLUDE_ANDROID_NOTIFICATION=true

# Disable push notifications via Tinode Push Gateway.
ENV TNPG_PUSH_ENABLED=false

# Tinode Push Gateway authentication token.
ENV TNPG_AUTH_TOKEN=

# Tinode Push Gateway organization name as registered at console.tinode.co
ENV TNPG_ORG=

# Video calls configuration.
ENV WEBRTC_ENABLED=false
ENV ICE_SERVERS_FILE=

# Use the target db by default.
# When TARGET_DB is "alldbs", it is the user's responsibility
# to set STORE_USE_ADAPTER to the desired db adapter correctly.
ENV STORE_USE_ADAPTER=$TARGET_DB

# Number of the goroutines processing topic master responses to the topic proxies.
# If 0, default value wll be used: # of nodes x 5.
ENV CLUSTER_NUM_PROXY_EVENT_GOROUTINES=0

# Url path for exposing the server's internal status. E.g. '/status'
ENV SERVER_STATUS_PATH=''

# Install root certificates, they are needed for email validator to work
# with the TLS SMTP servers like Gmail or Mailjet. Also add bash and grep.
RUN apk update && \
	apk add --no-cache ca-certificates bash grep

WORKDIR /opt/tinode

# Copy config template to the container.
COPY config.template .
COPY entrypoint.sh .

# Get the desired Tinode build.
ADD https://github.com/tinode/chat/releases/download/v$BINVERS/tinode-$TARGET_DB.linux-amd64.tar.gz .

# Unpack the Tinode archive.
RUN tar -xzf tinode-$TARGET_DB.linux-amd64.tar.gz \
	&& rm tinode-$TARGET_DB.linux-amd64.tar.gz

# Create directory for chatbot data.
RUN mkdir /botdata

# Make scripts runnable
RUN chmod +x entrypoint.sh
RUN chmod +x credentials.sh

# Generate config from template and run the server.
ENTRYPOINT ./entrypoint.sh

# HTTP, gRPC, cluster ports
EXPOSE 6060 16060 12000-12003
