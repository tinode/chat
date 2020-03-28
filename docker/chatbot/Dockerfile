# Dockerfile builds an image with a chatbot (Tino) for Tinode.

FROM python:3.7-slim

ARG VERSION=0.16
ARG LOGIN_AS=
ARG TINODE_HOST=tinode-srv:16060
ENV VERSION=$VERSION
ARG BINVERS=$VERSION

LABEL maintainer="Tinode Team <info@tinode.co>"
LABEL name="TinodeChatbot"
LABEL version=$VERSION

RUN mkdir -p /usr/src/bot

WORKDIR /usr/src/bot

# Volume with login cookie. Not created automatically.
# VOLUME /botdata

# Get tarball with the chatbot code and data.
ADD https://github.com/tinode/chat/releases/download/v${BINVERS}/py-chatbot.tar.gz .
# Unpack chatbot, delete archive
RUN tar -xzf py-chatbot.tar.gz \
	&& rm py-chatbot.tar.gz

RUN pip install --no-cache-dir -r requirements.txt

# Use docker's command line parameter `-e LOGIN_AS=user:password` to login as someone other than Tino.

CMD python chatbot.py --login-basic=${LOGIN_AS} --login-cookie=/botdata/.tn-cookie --host=${TINODE_HOST} >> /var/log/chatbot.log

# Plugin port
EXPOSE 40051
