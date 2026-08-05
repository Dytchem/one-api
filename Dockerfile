# syntax=docker/dockerfile:1
# dyt-29: pin base images to known-good versions (was node:16, golang:alpine, alpine:latest)
FROM node:20-alpine AS builder

WORKDIR /web
COPY ./VERSION .
COPY ./web .

# dyt-52: 顺序执行 + set -e，构建失败不再被 & wait 掩盖
# --legacy-peer-deps: react-scripts@5 与 i18next 的 typescript peer 冲突，CRA 仅在存在 ts 文件时需要
RUN set -e && npm install --prefix /web/default --no-audit --no-fund --legacy-peer-deps

RUN set -e && DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat ./VERSION) npm run build --prefix /web/default

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
