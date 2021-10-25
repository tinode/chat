# Accept the Go version for the image to be set as a build argument.
ARG GO_VERSION=1.16

# Execute the build using an alpine linux environment so that it will execute properly in the final environment
FROM golang:${GO_VERSION}-alpine AS builder

ENV CGO_ENABLED=0
ENV GO111MODULE=on
ENV GOFLAGS=-mod=vendor

# Create the user and group files that will be used in the running container to
# run the process as an unprivileged user.
RUN mkdir /user && \
    echo 'nobody:x:65534:65534:nobody:/:' > /user/passwd && \
    echo 'nobody:x:65534:' > /user/group

# Install the Certificate-Authority certificates for the app to be able to make
# calls to HTTPS endpoints.
# Git is required for fetching the dependencies.
RUN apk add ca-certificates

# Set a directory to contain the go app to be compiled (this directory will work for go apps making use of go modules as long as go 1.13+ is used)
RUN mkdir -p /go/src/mgi-vn/chat
WORKDIR /go/src/mgi-vn/chat

COPY ./server .
COPY go.mod .
COPY go.sum .
COPY ./vendor .

RUN go get github.com/mitchellh/gox
RUN gox -osarch="linux/amd64" \
        -ldflags "-s -w -X main.buildstamp=`git describe --tags`" \
        -tags "mongodb" -output /dist /go/src/mgi-vn/chat > /dev/null

RUN cp tinode.conf /dist/
RUN cp .env /dist/
RUN cp templ/*.templ /dist/

# Final stage: the running container.
FROM alpine AS final

# Import the user and group files from the first stage.
COPY --from=builder /user/group /user/passwd /etc/

# Import the Certificate-Authority certificates for enabling HTTPS.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Import the compiled executable from the first stage.
COPY --from=builder /dist /chat

# Import the compiled grpc-health-probe from the first stage
# COPY --from=builder /go/bin/grpc-health-probe /usr/bin/local/grpc-health-probe

# Perform any further action as an unprivileged user.
USER nobody:nobody

# Run the compiled binary.
ENTRYPOINT ["/chat"]
EXPOSE 6060 16060 12000-12003
