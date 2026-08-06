# syntax=docker/dockerfile:1
# dyt-29: pin base images to known-good versions (was node:16, golang:alpine, alpine:latest)
FROM node:20-alpine AS builder

WORKDIR /web

# dyt-55: npm 源固定为 npmmirror，构建机无代理也能快速拉取
ENV npm_config_registry=https://registry.npmmirror.com

# dyt-55: 依赖层单独缓存 —— 仅 package 文件变化才重装依赖
# npm ci 使用提交的 package-lock.json 固定依赖树（修复 ajv 缺失）；
# --legacy-peer-deps 忽略 react-scripts@5 与 i18next 的 typescript peer 冲突（CRA 仅在有 ts 文件时需要）
COPY ./web/default/package.json ./web/default/package-lock.json /web/default/
RUN set -e && npm ci --prefix /web/default --no-audit --no-fund --legacy-peer-deps

# 源码层：源码变化只重跑构建，不重装依赖
COPY ./VERSION .
COPY ./web .

# dyt-55: 预建产物目录（.dockerignore 不再携带 web/build，mv 目标父目录需存在）
RUN set -e && mkdir -p /web/build && DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat ./VERSION) INLINE_RUNTIME_CHUNK='false' npm run build --prefix /web/default

# pi-bridge（内置 agent/chat 后台会话服务）：仅依赖文件变化时重装
COPY ./pi-bridge/package.json ./pi-bridge/package-lock.json /pi-bridge/
RUN set -e && npm ci --prefix /pi-bridge --no-audit --no-fund --omit=dev
COPY ./pi-bridge/server.js /pi-bridge/

FROM golang:1.22-alpine3.20 AS builder2

RUN apk add --no-cache \
    gcc \
    musl-dev \
    sqlite-dev \
    build-base

ENV GO111MODULE=on \
    CGO_ENABLED=1 \
    GOOS=linux \
    GOPROXY=https://goproxy.cn,direct

WORKDIR /build

ADD go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=builder /web/build ./web/build

RUN go build -trimpath -ldflags "-s -w -X 'github.com/songquanpeng/one-api/common.Version=$(cat VERSION)' -linkmode external -extldflags '-static'" -o one-api

FROM node:20-alpine

RUN apk add --no-cache ca-certificates tzdata

# pi-bridge：agent / 聊天后台会话服务（与 one-api 同容器，由 entrypoint 一并拉起）
COPY --from=builder /pi-bridge /pi-bridge
COPY ./pi-bridge/server.js /pi-bridge/server.js
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

COPY --from=builder2 /build/one-api /

EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/entrypoint.sh"]
