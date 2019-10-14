# Dockerfile builds an image with a chatbot (Tino) for Tinode.

FROM python:slim

ARG VERSION=0.16
ENV VERSION=$VERSION

LABEL maintainer="Tinode Team <info@tinode.co>"
LABEL name="TinodeChatbot"
LABEL version=$VERSION

RUN mkdir -p /usr/src/bot
WORKDIR /usr/src/bot

RUN pip install --no-cache-dir -q tinode-grpc

# Get tarball with the chatbot code and data.
ADD https://github.com/tinode/chat/releases/download/v$VERSION/py-chatbot.tar.gz .
# Unpack chatbot, delete archive
RUN tar -xzf py-chatbot.tar.gz \
	&& rm py-chatbot.tar.gz

# Use command line parameter `-e LOGIN_AS=user:password` to login as someone other than Tino.

CMD python chatbot.py --login-basic=${LOGIN_AS} --login-cookie=/botdata/.tn-cookie --host=tinode-srv:16061 > /var/log/chatbot.log

# Plugin port
EXPOSE 40051
