<p align="right">
   <a href="./README.en.md">English</a>
</p>

<p align="center">
  <a href="https://github.com/Dytchem/one-api"><img src="https://raw.githubusercontent.com/songquanpeng/one-api/main/web/default/public/logo.png" width="120" height="120" alt="one-api logo"></a>
</p>

<div align="center">

# One API

**统一管理 AI 渠道与令牌的网关，内置 Chat 与 AI Agent**

在线体验：[oneapi.dytchem.cn](https://oneapi.dytchem.cn)

</div>

<p align="center">
  <a href="https://github.com/Dytchem/one-api/releases/latest">
    <img src="https://img.shields.io/github/v/release/Dytchem/one-api?color=brightgreen&include_prereleases" alt="release">
  </a>
  <a href="https://github.com/Dytchem/one-api/pkgs/container/one-api">
    <img src="https://img.shields.io/docker/pulls/ghcr.io/dytchem/one-api?color=brightgreen" alt="docker pull">
  </a>
</p>

---

## 这是什么

一个 AI 网关服务：把各类模型渠道（DeepSeek、MiMo、Gemini、Agnes、OpenCode 等）统一接入，用一套令牌对外提供 OpenAI 兼容接口。

Fork 自 [songquanpeng/one-api](https://github.com/songquanpeng/one-api)，在此基础上深度定制。

## 功能

- **渠道管理**：多渠道接入、自动测速、优先级与权重、模型映射
- **令牌管理**：一键生成多令牌，额度/速率控制，独立计费与日志
- **内置 Chat**：网页聊天，支持多模态附件、思考等级、指定渠道
- **内置 Agent**：网页 AI Agent，工具调用操作 one-api（查渠道、测渠道、管理渠道、查日志等），请求后台执行、断点续传
- **统一画布**：所有设备渲染同一 1440px 画布，任意端所见一致
- **单容器部署**：Chat/Agent 后台服务内置同一镜像，一条命令启动

## 快速开始

```bash
# 1. 启动（默认端口 3000）
docker run -d --name one-api --restart unless-stopped \
  -p 3000:3000 \
  -e SQL_DSN='user:password@tcp(mysql:3306)/one-api?charset=utf8mb4&parseTime=True&loc=Local' \
  -e SESSION_SECRET='replace-with-a-long-random-string' \
  ghcr.io/dytchem/one-api:v0.6.11-dyt-81
```

> 也可使用 SQLite（不传 `SQL_DSN` 即可，数据存 `/data`，建议挂载卷）。

启动后访问 `http://<host>:3000`，初始账号 `root`，密码在启动日志中。

### 使用 Agent

Agent 需要配置管理员令牌用于工具调用：

```bash
-e ONEAPI_ADMIN_TOKEN='sk-xxxx'        # 管理员令牌（Agent 工具全权限）
-e AGENT_BRIDGE_URL='http://127.0.0.1:3005'   # 默认即可，勿改
```

### 代理出口

```bash
-e HTTP_PROXY='http://127.0.0.1:8118' \
-e HTTPS_PROXY='http://127.0.0.1:8118' \
-e NO_PROXY='localhost,127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16'
```

## 接口

OpenAI 兼容：`POST /v1/chat/completions`、`POST /v1/models` 等，`Authorization: Bearer <令牌>`。

## 镜像

- GitHub Container Registry：`ghcr.io/dytchem/one-api`
- 当前版本：`v0.6.11-dyt-81`，跟随 [Releases](https://github.com/Dytchem/one-api/releases)

## License

MIT
