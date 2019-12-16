FROM golang:1.13-alpine AS src

RUN apk update && apk upgrade; \
    apk add --update --no-cache --repository http://dl-3.alpinelinux.org/alpine/edge/community --repository http://dl-3.alpinelinux.org/alpine/edge/main vips-dev; \
    apk add build-base

WORKDIR /go/src/github.com/conves/imgrsz/
COPY ./go.mod ./go.sum ./
COPY ./cmd/resizer ./cmd/resizer
COPY ./internal ./internal

RUN cd ./cmd/resizer && GOOS=linux go build -o ./resizerd;

# Final image, no source code
FROM alpine:latest

RUN apk update && apk upgrade; \
    apk add --update --no-cache --repository http://dl-3.alpinelinux.org/alpine/edge/community --repository http://dl-3.alpinelinux.org/alpine/edge/main vips-dev; \
    apk add build-base

WORKDIR .
COPY --from=src /go/src/github.com/conves/imgrsz/cmd/resizer/resizerd .

EXPOSE 8080

# Run Go Binary
CMD ./resizerd