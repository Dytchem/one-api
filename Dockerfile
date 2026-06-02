# syntax=docker/dockerfile:1
# dyt-29: pin base images to known-good versions (was node:16, golang:alpine, alpine:latest)
FROM node:20-alpine AS builder

WORKDIR /web
COPY ./VERSION .
COPY ./web .

RUN npm install --prefix /web/default & \
    echo "berry disabled" & \
    echo "air disabled" & \
    wait

RUN DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat ./VERSION) npm run build --prefix /web/default & \
    DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat ./VERSION) echo "berry disabled" & \
    DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat ./VERSION) echo "air disabled" & \
    wait

FROM golang:1.22-alpine3.20 AS builder2

RUN apk add --no-cache \
    gcc \
    musl-dev \
    sqlite-dev \
    build-base

ENV GO111MODULE=on \
    CGO_ENABLED=1 \
    GOOS=linux

WORKDIR /build

ADD go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=builder /web/build ./web/build

RUN go build -trimpath -ldflags "-s -w -X 'github.com/songquanpeng/one-api/common.Version=$(cat VERSION)' -linkmode external -extldflags '-static'" -o one-api

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder2 /build/one-api /

EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/one-api"]
