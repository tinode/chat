FROM grpc/python

LABEL maintainer="Gene Sokolov <gene@tinode.co>"
LABEL name="TinodeChatbot"
LABEL version="0.14.1"

RUN mkdir -p /usr/src/bot
WORKDIR /usr/src/bot

ADD https://raw.githubusercontent.com/tinode/chat/master/chatbot/chatbot.py 	/usr/src/bot
# ADD https://raw.githubusercontent.com/tinode/chat/devel/chatbot/chatbot.py 	/usr/src/bot
ADD https://raw.githubusercontent.com/tinode/chat/master/chatbot/quotes.txt 	/usr/src/bot
ADD https://raw.githubusercontent.com/tinode/chat/master/pbx/model_pb2.py 		/usr/src/bot
ADD https://raw.githubusercontent.com/tinode/chat/master/pbx/model_pb2_grpc.py 	/usr/src/bot

# Use command line parameter `-e LOGIN_AS=user:password` to login as someone other than Tino.

ENTRYPOINT python chatbot.py --login-basic=${LOGIN_AS} --login-cookie=/botdata/.tn-cookie --host=tinode-srv:16061

# Plugin port
EXPOSE 40051
