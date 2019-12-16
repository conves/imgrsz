FROM golang:1.13-alpine AS src

RUN apk update && apk upgrade; \
    apk add build-base

WORKDIR /go/src/github.com/conves/imgrsz/
COPY ./go.mod ./go.sum ./
COPY ./cmd/api ./cmd/api
COPY ./internal ./internal

RUN cd ./cmd/api && GOOS=linux go build -o ./imgrsz;

# Final image, no source code
FROM alpine:latest

RUN apk update && apk upgrade; \
    apk add build-base

WORKDIR .
COPY --from=src /go/src/github.com/conves/imgrsz/cmd/api/imgrsz .

EXPOSE 8080

# Run Go Binary
CMD ./imgrsz