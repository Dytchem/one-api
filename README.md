<p align="right">
   <a href="./README.en.md">English</a>
</p>

<p align="center">
  <a href="https://github.com/songquanpeng/one-api"><img src="https://raw.githubusercontent.com/songquanpeng/one-api/main/web/default/public/logo.png" width="120" height="120" alt="one-api logo"></a>
</p>

<div align="center">

# One API — Fork

_Fork 自 [songquanpeng/one-api](https://github.com/songquanpeng/one-api)，自用定制_

</div>

<p align="center">
  <a href="https://github.com/Dytchem/one-api/releases/latest">
    <img src="https://img.shields.io/github/v/release/Dytchem/one-api?color=brightgreen&include_prereleases" alt="release">
  </a>
  <a href="https://github.com/Dytchem/one-api/pkgs/container/one-api">
    <img src="https://img.shields.io/docker/pulls/ghcr.io/dytchem/one-api?color=brightgreen" alt="docker pull">
  </a>
</p>

> [!NOTE]
> 本 Fork 偏**自用定制**，部分功能（尤其是计费相关）可能与上游不一致，请以测试为准。

## 主要改进

### Fallback / 探测

| 改进 | 说明 |
|------|------|
| Fallback 增强 | 重试时恢复原始模型名，维护失败渠道集合，支持优先级降级 |
| Stream Probe | 流式响应空内容探测，避免半途断流；TTFT 先行写入日志 |
| 探测失败记录上游细节 | 日志附 `\| 上游：连接断/异常结束 (HTTP 200, 6行/1887字节, 末行: ...)` 真实断流位置 |
| empty response 同渠道重试 | 探测空响应时同渠道重试 1 次（dyt-18 引入，dyt-23 修复 body 一致性）|
| 探测 body 完全一致 | 两次 doProbe 用同一份 body bytes，避免 adaptor 转换丢字段触发 2013（dyt-23）|
| 探测承认 tool_calls | 流式 tool_calls 时 `delta` 只有 `tool_calls` 不发 content，探测不再误判为空响应（dyt-26）|

### 渠道健康度 / 性能

| 改进 | 说明 |
|------|------|
| 渠道健康度 | 滑动窗口追踪 + 连续失败熔断，tok/s 性能指标 |

### 日志系统

| 改进 | 说明 |
|------|------|
| 日志详情统一 | 四类统一格式（探测成功/失败/回复完成/测试完成），全部显示 `请求模型：渠道名/模型名` |
| 类型徽章 | 默认主题新增 成功/失败/完成/测试 四种彩色徽章 |
| 回复内容记录 | 非流式请求日志记录真实 AI 回复（非用户 prompt）|
| 耗时/流式标签 | 详情列独立显示耗时徽章 + Stream/Non-Stream 标识 |
| 末行预览 200 字节 | 流式失败时末行预览从 60→200 字节，含 tool_calls 截断信息 |
| 状态码徽章 | 失败日志加 `[2013:invalid params]` 类状态码徽章 |
| 格式统一 | `模型：jarvis→MiniMax-M3`（用户原始 → 实际路由）覆盖探测/失败/渠道尝试日志 |
| 失败去重 | empty_response 场景不再产生重复日志（探测失败 + 渠道尝试两条变一条）|

### 失败调试（dyt-20+）

| 改进 | 说明 |
|------|------|
| 失败 payload 完整保留 | 失败请求/响应/错误完整存入 `log_payloads` 表，request 可达 ~700KB |
| 流异常检测 | `finish_reason: tool_calls` 但响应截断，自动判定为流异常 |
| 错误日志按 attempt 拆分 | `[原始请求]` / `[重试-1]` 两条独立错误日志，含完整响应体 |
| UI 失败日志浏览器 | `/fail-logs` 路由，按时间倒序列出失败，支持按渠道/模型筛选 |
| payload 在线查看 | 点击日志行展开完整 JSON（请求+响应+错误），可直接复制 |
| CHANNEL_RETRY_COUNT | 环境变量控制同渠道重试次数（默认 1）|

### CI/CD

| 改进 | 说明 |
|------|------|
| 构建优化 | 移除 QEMU 多架构、启用 Docker 层缓存，构建时间从 6min→~3min |
| 仅 tag 触发 | 推送 tag 才构建，普通 commit 不触发 |

### 安全加固（dyt-27）

| 改进 | 说明 |
|------|------|
| CORS 白名单 | 移除 `AllowAllOrigins + AllowCredentials` 同开的 CSRF 风险，通过 `ALLOWED_ORIGINS` 环境变量配置（逗号分隔），未配置时降级为任意 origin 不带 cookie |
| SMTP TLS 严格校验 | 移除 `InsecureSkipVerify: true`，强制 TLS 证书校验（主人自部署 SMTP 应有合规证书）|
| 加密随机数 | `common/random` 从 `math/rand` 升级为 `crypto/rand`，所有 token/key 不可预测（旧 token 仍有效）|

#### 安全环境变量（dyt-27 新增）

| 变量 | 含义 | 默认 |
|------|------|------|
| `ALLOWED_ORIGINS` | CORS 白名单 origin，逗号分隔（如 `https://one.example.com,https://two.example.com`）；**留空** = 任意 origin 跨域（不带 cookie，安全降级） | 留空 |
| `SMTP_SKIP_VERIFY` | （未启用）SMTP TLS 跳过证书校验；如需临时回退请编辑 `common/message/email.go` | — |

> dyt-27 是安全修复版本，**不影响**任何 token 兼容性、API 接口、日志格式。

### 依赖升级（dyt-28）

| 改进 | 说明 |
|------|------|
| Go 1.20 → 1.22 | Go 1.20 已 EOL（2024-08），升至 1.22 LTS |
| gin 1.10.0 → 1.10.1 | patch 版本，零 API 变化 |
| sonic 1.11.6 → 1.12.5 | 修 GHSA-8633-2w75-77qx ReDoS（sonic 是 gin 的间接依赖）|
| Dockerfile pin | 留待 dyt-29：pin base image 为 `golang:1.22-alpine3.20` / `alpine:3.20` / `node:20-alpine` |

dyt-28 代码改动 = 0，纯依赖版本号升级，零回归风险。

完整变更记录 → [GitHub Releases](https://github.com/Dytchem/one-api/releases)

## 快速部署

```bash
docker pull ghcr.io/dytchem/one-api:latest
docker run -d --name one-api -p 3000:3000 \
  -e SQL_DSN='user:password@tcp(host:3306)/one-api' \
  ghcr.io/dytchem/one-api:latest
```

> ⚠️ 初次部署后务必修改默认密码 `123456`。

> ⚠️ 升级到 dyt-20+ 时，`log_payloads` 表会自动迁移（旧日志会丢失 payload，只有新失败才存）。

## 版本号约定

```
v0.6.11-dyt-N    # N 为自增构建号，每次发布递增
```

当前最新：**[v0.6.11-dyt-26](https://github.com/Dytchem/one-api/releases/tag/v0.6.11-dyt-26)**

## 失败日志调试工作流（dyt-20+）

1. 进入 `/fail-logs` 路由，按时间倒序看失败列表
2. 失败日志带：
   - 渠道 + 模型（`渠道：MiniMax Frontier(#27)，模型：jarvis→MiniMax-M3`）
   - 上游错误细节（`\| 上游：连接断/异常结束...`）
   - 状态码徽章（`[2013:invalid params]`）
3. 点击行展开 → 看完整 request JSON（最大 700KB）
4. 把完整 JSON 贴给上游厂商工单（含 tools schema 完整定义）

## 工具调用流兼容（dyt-26 修复）

OneAPI 上游 stream probe 只承认 `delta.content` 和 `delta.reasoning_content` 为有效响应。当模型流式返回 tool_calls（每个 chunk 只发 `delta.tool_calls`，不发 `delta.content`）时会被误判为空响应。

dyt-26 在探测判断里增加 `len(delta.ToolCalls) > 0` 分支，**所有使用 OpenAI 兼容 tool_calls API 的模型**（M3、M2.7、Qwen 等）流式调用成功率显著提升。

## 技术栈

由 **[OpenClaw](https://github.com/openclaw/openclaw)** AI Agent 协助开发维护。

---

*上游项目：[songquanpeng/one-api](https://github.com/songquanpeng/one-api)，MIT 协议。*
