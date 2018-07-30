# Dockerfile builds an image with a chatbot (Tino) for Tinode.

FROM python:3

ARG VERSION=0.14.5
ENV VERSION=$VERSION

LABEL maintainer="Gene Sokolov <gene@tinode.co>"
LABEL name="TinodeChatbot"
LABEL version=$VERSION

RUN mkdir -p /usr/src/bot
WORKDIR /usr/src/bot

COPY requirements.txt .

RUN pip install --no-cache-dir -q -r requirements.txt

# Get tarball with the chatbot code and data.
ADD https://github.com/tinode/chat/releases/download/v$VERSION/chatbot.tar.gz .
# Unpack chatbot, delete archive
RUN tar -xzf chatbot.tar.gz \
	&& rm chatbot.tar.gz

# Use command line parameter `-e LOGIN_AS=user:password` to login as someone other than Tino.

CMD python chatbot.py --login-basic=${LOGIN_AS} --login-cookie=/botdata/.tn-cookie --host=tinode-srv:16061 > /var/log/chatbot.log

# Plugin port
EXPOSE 40051
