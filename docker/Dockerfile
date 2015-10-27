# Docker file builds a container with a tinode server. The server is accessible at the port 8080

FROM golang

MAINTAINER Gene Sokolov <or.else@gmail.com>

RUN go get github.com/tinode/chat/server

RUN go install github.com/tinode/chat/server

EXPOSE 8080

ENTRYPOINT ["/go/bin/server", "--bind :8080", "--db rethinkdb://localhost:28015/tinode?authKey=&discover=false&maxIdle=&maxOpen=&timeout=&workerId=1&uidkey=la6YsO-bNX_-XIkOqc5Svw=="]

