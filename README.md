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

Fork 自 [songquanpeng/one-api](https://github.com/songquanpeng/one-api)，在此基础上深度定制：内置 Chat 与 Agent 能力、渠道与探测机制完善、日志系统重构、安全加固、统一画布界面。

## 主要变更（自 fork 以来）

### 内置 Chat 与 Agent

- **内置 Chat**：网页聊天，支持多模态附件（图片/音频/视频）、思考等级、指定渠道与令牌
- **内置 AI Agent**：工具调用直接操作 one-api（查询/测试/管理渠道、查看日志等）
- **后台执行**：请求在 bridge 后台运行，UI 只是订阅者——刷新/离开页面不中断生成，回来自动续传
- **零配置部署**：Chat/Agent 服务内置同一镜像，一条命令启动

### 渠道与模型

- 新增 12+ 常用提供商渠道模板与模型建议
- 支持 OpenAI Responses API
- 模型映射可视化编辑、一键拉取模型列表
- 渠道复制、渠道排序白名单
- 探测与 fallback 机制完善：失败自动禁用渠道（可关闭）、空响应自动重试、tool_calls 流不再误判失败、探测超时可配置

### 日志系统

- 日志详情显示真实内容（探测显示请求、消费显示回复）
- 失败请求/响应完整保留（log_payloads）+ UI 失败日志页 + 流异常检测
- 记录 cache_read/cache_creation tokens（不计费）
- log_payloads 自动清理（默认 7 天 TTL，`LOG_PAYLOAD_TTL_HOURS` 可配）
- 用户断开请求时同步断开上游，新增对应日志类型
- 日志 UI 细节：tok/s 列、列宽与对齐、状态徽章、按 attempt 拆分错误

### 安全加固

- 依赖升级：Go 1.22、gin、sonic、golang-jwt v5、marked
- Dockerfile 固定基础镜像版本，可复现构建
- CORS 白名单、SMTP TLS 严格校验、crypto/rand 替换 math/rand
- panic 不再打印 request body、RelayTimeout 默认 300s
- Cookie 安全配置、防邮箱枚举、禁止用户主动清 token 缓存、启动日志不打印默认密码

### 界面与体验

- 统一画布：所有设备渲染同一 1440px 画布，任意端所见一致
- 手机端布局修复、页面白屏防护

### 部署与运维

- 自用模式：移除计费逻辑
- 单容器部署（Chat/Agent 与网关同镜像）、CI/CD 构建优化、语义化版本管理

### 性能（v100）

- 认证链路进程内 TTL 缓存（Redis 关闭也生效），每请求 DB 往返 ~11 次 → ~4 次
- 健康路由复用健康分 + 内存渠道快照；健康分原子快照 O(1) 无锁读
- 消费日志批量写（100ms 合并 INSERT，username 一次 IN 回填）；SSE 每行只解析一次
- pi-bridge 脏会话标记 + 异步落盘（不阻塞流式输出）；SSE 头先发（模型同步不阻塞首字节）
- 连接池默认贴合 MySQL max_connections；日志表三合一索引

## 快速开始

```bash
# 最简启动（host 网络 + MySQL + 代理），bridge 用默认端口 3005
docker run -d --name one-api --restart unless-stopped --network host \
  -v /data:/data \
  -e SQL_DSN='user:password@tcp(127.0.0.1:3306)/one-api?charset=utf8mb4&parseTime=True&loc=Local' \
  -e PORT=3004 \
  -e ONEAPI_BASE='http://127.0.0.1:3004' \
  -e HTTP_PROXY='http://127.0.0.1:8118' \
  -e HTTPS_PROXY='http://127.0.0.1:8118' \
  -e NO_PROXY='localhost,127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16' \
  ghcr.io/dytchem/one-api:latest
```

> - `PORT` 非默认（3000）时必须同时设置 `ONEAPI_BASE` 与它同值（bridge 同步模型用）
> - 宿主机 3005 端口被占时，加 `-e AGENT_BRIDGE_URL='http://127.0.0.1:3006' -e BRIDGE_PORT=3006`
> - 安全可选：`BRIDGE_SECRET`/`AGENT_BRIDGE_SECRET` 成对配置（同值）启用 bridge 鉴权；不配也能正常工作（兼容模式）
> - 也可使用 SQLite（不传 `SQL_DSN` 即可，数据存 `/data`，建议挂载卷）

启动后访问 `http://<host>:3004`，初始账号 `root`，密码在启动日志中。

### 使用 Agent

Agent 无需额外配置：工具调用凭据与模型表同步都使用**登录用户自己的令牌**（前端自动选用当前账号的第一个可用令牌），模型列表自动同步，无需任何部署级 key。

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
- 当前版本：见根目录 [VERSION](VERSION) 文件（构建时自动注入 UI 与镜像 tag），发布记录见 [Releases](https://github.com/Dytchem/one-api/releases)（仅保留里程碑版本，历史镜像 tag 均可 `docker pull` 获取，如 `ghcr.io/dytchem/one-api:v0.6.11-dyt-94`）

## License

MIT
