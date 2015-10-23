FROM golang

ADD . /go/src/github.com/tinode/chat/server

RUN go get github.com/dancannon/gorethink
RUN go get github.com/gorilla/websocket
RUN go get github.com/tinode/snowflake
RUN go get golang.org/x/crypto/bcrypt
RUN go get golang.org/x/crypto/xtea

RUN go install github.com/tinode/chat/server
ENTRYPOINT /go/bin/server --bind 127.0.0.1:8088 --db rethinkdb://localhost:28015/tinode?authKey=&discover=false&maxIdle=&maxOpen=&timeout=&workerId=1&uidkey=la6YsO-bNX_-XIkOqc5Svw==

EXPOSE 8080
