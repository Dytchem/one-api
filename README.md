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

### 镜像基础版本（dyt-29）

| 改进 | 说明 |
|------|------|
| Node 16 → 20 | Node 16 已 EOL（2023-09），升至 Node 20 LTS |
| `golang:alpine` → `golang:1.22-alpine3.20` | pin Go 1.22 + Alpine 3.20 |
| `alpine:latest` → `alpine:3.20` | pin Alpine 3.20 运行时 |

dyt-29 代码改动 = 0（仅 Dockerfile），运行时改为可复现的已知安全版本。

### 库升级（dyt-30）

| 改进 | 说明 |
|------|------|
| golang-jwt v3 → v5.3.1 | v3 已 EOL，v5 API 兼容（只改 import path 1 行）|
| marked ^4.1.1 → ^4.3.0 | 修 GHSA-rrrm-qjm4-v8hf ReDoS；保守升 v4.x 末版，零 API 变化 |

dyt-30 代码改动 = 1 行（zhipu adaptor 的 import path），零回归风险。

---

## 🛡️ 安全修复总览（dyt-27 ~ dyt-30）

| ID | 严重度 | 修复 | dyt |
|----|--------|------|-----|
| S1 | 🔴 高 | CORS 拆除 `AllowAllOrigins+AllowCredentials` | dyt-27 |
| S2 | 🔴 高 | SMTP TLS 移除 `InsecureSkipVerify` | dyt-27 |
| S3 | 🔴 高 | `math/rand` → `crypto/rand` | dyt-27 |
| S4 | 🟠 中 | bytedance/sonic 1.11.6 → 1.12.5（修 GHSA-8633-2w75-77qx）| dyt-28 |
| S5 | 🟠 中 | golang-jwt v3 EOL → v5.3.1 | dyt-30 |
| S6 | 🟠 中 | Go 1.20 EOL → 1.22 | dyt-28 |
| S7 | 🟠 中 | Dockerfile pin base image（node:20-alpine, golang:1.22-alpine3.20, alpine:3.20）| dyt-29 |
| S8 | 🟡 低 | marked ReDoS（GHSA-rrrm-qjm4-v8hf） | dyt-30 |

**所有 8 个安全问题已修复**。4 个 dyt tag，累计代码改动 ~10 行，**0 业务行为变化**。

### Round 2 安全加固（dyt-31~dyt-33）

| ID | 严重度 | 修复 | dyt |
|----|--------|------|-----|
| R2-S1 | 🟡 低 | SessionSecret 环境变量支持（已在 common/init.go 原有，加 README 说明） | dyt-31 |
| R2-S2 | 🟠 中 | RelayTimeout 默认 0→300s（防恶意上游卡死 worker）| dyt-31 |
| R2-S3 | 🟠 中 | panic 时请求 body 不再打到日志（防敏感信息泄露）| dyt-32 |
| R2-S6 | 🟡 低 | log_payloads 清理 + README 说明 | dyt-33 |

#### 新增环境变量（dyt-31~dyt-32）

| 变量 | 含义 | 默认 |
|------|------|------|
| `SESSION_SECRET` | Session 加密密钥（≥32 字符随机串）；留空=每次启动 uuid 随机（重启后所有用户登出）| 留空 |
| `RELAY_TIMEOUT` | 转发请求超时（秒）| 300 |
| `PANIC_LOG_BODY` | panic 时是否打请求 body 到日志（设 true 时打）| false |
| `LOG_PAYLOAD_TTL_HOURS` | log_payloads 保留小时数；设 -1 禁用清理 | 168 (7天) |

完整变更记录 → [GitHub Releases](https://github.com/Dytchem/one-api/releases)

### Round 3 安全加固（dyt-34~dyt-35）

| ID | 严重度 | 修复 | dyt |
|----|--------|------|-----|
| R3-S1 | 🟠 中 | cookie 显式 HttpOnly+SameSite+MaxAge、新增 `SESSION_COOKIE_SECURE` 环境变量 | dyt-34 |
| R3-S2 | 🟠 中 | SendPasswordResetEmail 防邮箱 oracle（存在/不存在都返回 success） | dyt-34 |
| R3-S3 | 🟠 中 | 禁用用户时主动清 Redis `user_enabled` 缓存，不再等 SyncFrequency（默认 10 分钟） | dyt-35 |
| R3-S6 | 🟡 低 | 启动日志 `password is 123456` → `default password is shown in README` | dyt-35 |

#### 更多环境变量（dyt-34）

| 变量 | 含义 | 默认 |
|------|------|------|
| `SESSION_COOKIE_SECURE` | cookie Secure 标记（主人用 HTTPS 时设 true）| false |

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

## dyt-53 OpenAI Responses API + 新提供商

### Responses API（/v1/responses）

自用网关新增 **OpenAI Responses API** 支持（`client.responses.create` 可直接使用）：

| 能力 | 说明 |
|------|------|
| 请求转换 | `input`/`instructions`/`tools`/`max_output_tokens` 自动转 chat 格式，复用探测与跨渠道 fallback |
| 非流式 | chat JSON → Responses JSON（output/usage/function_call 完整转换，usage 缺失时按文本兜底估算）|
| 流式 | chat SSE → Responses SSE（`response.created/delta/done/completed` 标准事件序列，含 `event:` 行）|
| 工具调用 | 流式 `function_call_arguments.delta/done`（按 index 归并增量参数）|
| 空响应 | 无内容无 token 时触发跨渠道 fallback |

> 仅 OpenAI 兼容渠道支持 Responses；上游 URL 自动改写为 `/chat/completions`。

### 新提供商渠道（dyt-53 新增）

| 渠道 | 类型 | 说明 |
|------|------|------|
| Perplexity | 52 | AI 搜索 API（Sonar 系列）|
| MokaAI | 53 | 国内模型聚合（api.mok.ai）|
| Xinference | 54 | 本地/远程推理服务（需自填 base_url）|
| Cerebras | 55 | 高速推理（gpt-oss 系列）|
| Hyperbolic | 56 | 分布式推理（开源模型）|
| Fireworks AI | 57 | 推理/微调平台 |
| Lambda | 58 | 开源模型推理 |
| 智谱 GLM (OpenAI 兼容) | 59 | open.bigmodel.cn/api/paas/v4 |
| Jina | 60 | 多模态/嵌入 |

### 基础 URL 更新（dyt-53）

| 渠道 | 旧 | 新 |
|------|----|----|
| MiniMax | api.minimax.chat（已停用）| api.minimaxi.com |
| 腾讯混元 | hunyuan.tencentcloudapi.com（TC3 已停售）| api.hunyuan.cloud.tencent.com/v1（OpenAI 兼容）|
| 360 智脑 | ai.360.cn | api.360.cn |
| Coze | api.coze.com | api.coze.cn |

## 版本号约定

```
v0.6.11-dyt-N    # N 为自增构建号，每次发布递增
```

当前最新：**[v0.6.11-dyt-60](https://github.com/Dytchem/one-api/releases/tag/v0.6.11-dyt-59)**

## dyt-52 自用模式：计费移除 + 修复

> 本版本起定位为**自用 LLM API 管理网关**，计费功能全部移除。

### 计费移除

| 改动 | 说明 |
|------|------|
| 配额扣减移除 | 预扣/实扣/退费全部 no-op，请求不再受余额限制（`relay/billing` 保留签名）|
| 兑换码系统移除 | `/redemption` 路由、页面、控制器、模型全部删除 |
| 充值系统移除 | `/topup` 路由、页面、`TopUp`/`AdminTopUp` 删除 |
| 计费配置项移除 | 设置页删除额度/倍率/充值链接/货币显示等配置 |
| 用户/令牌额度 UI 移除 | 表格余额列替换为请求数统计 |
| dashboard 统计保留 | 日志 `quota` 字段改为记录 **token 总量**，统计图表继续可用 |
| 渠道余额查询保留 | `update_balance`（上游余额查看）仍在 |

> DB 兼容：`quota`/`remain_quota` 等字段保留在表中（不再扣减），可直接复用现有数据库。

### Bug 修复（P0/P1）

| 修复 | 说明 |
|------|------|
| 健康选渠道条件反转 | `middleware/distributor.go` 条件 `!= nil` → `== nil`，健康排序/熔断过滤现在真正生效 |
| 失败日志 SQL 优先级 | `GetFailLogs` 加括号，不再捞错类型日志 |
| 渠道排序 SQL 注入 | `order`/`sort` 白名单校验，点"健康度"表头不再 500 |
| probe 渠道兼容 | 探测仅对 OpenAI 兼容渠道启用；Anthropic/Gemini 等不再 120s 超时 |
| 流式透传后误判失败 | 数据已发给客户端后不再判定失败触发重试（防重复内容）|
| 双 `[DONE]` 哨兵 | 上游已发 `[DONE]` 不再补发 |
| keep-alive 超时短路 | 排队场景使用 3×PROBE_TIMEOUT 期限，不再被 120s 短路 |
| 400 重试 | 无效请求不再烧光重试次数 |
| 空响应误判 | 非流式空响应判定收紧（仅 usage 全 0 且无内容），不再误伤 embedding |
| 499 语义 | 服务端超时不再误记为"用户断开" |
| 死代码 | 删除 `StreamHandlerWithBuffer`，统一 scanner 逻辑 |
| metric goroutine 堆积 | `Emit` 改非阻塞发送 |
| N+1 查询 | 失败日志列表批量查 payload |
| 前端 `JSON.parse` 崩溃 | EditChannel 脏 JSON 不再卡死页面 |
| 前端 i18n 缓存 | 渠道类型标签切换语言后正常刷新 |
| 分页公式 | 数据整倍数时不再出现空页 |
| Dockerfile 构建掩盖 | 移除 `& wait` 并行占位，失败立即报错 |

### 新能力（参考 new-api 实践）

| 变量 | 含义 | 默认 |
|------|------|------|
| `STREAM_SCANNER_MAX_BUFFER_MB` | 流式 SSE 单行缓冲上限（base64 大图不截断）| 64 |
| `MAX_REQUEST_BODY_MB` | 请求体大小上限（防超大请求/zip bomb）| 32 |
| `STREAMING_TIMEOUT` | 流式透传整体超时（秒），0 = 跟随 HTTPClient | 0 |

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

### 常用提供商补充（dyt-54）

基于 models.dev 数据（opencode 内置的 180 个提供商库）补充 12 个常用渠道：

| 渠道 | 类型 | 端点 |
|------|------|------|
| 小米 MiMo | 61 | api.xiaomimimo.com/v1 |
| OpenCode Zen | 62 | opencode.ai/zen/v1 |
| OpenCode Go | 63 | opencode.ai/zen/go/v1 |
| Ollama Cloud | 64 | ollama.com/v1 |
| NVIDIA NIM | 65 | integrate.api.nvidia.com/v1 |
| Hugging Face | 66 | router.huggingface.co/v1 |
| ModelScope 魔搭 | 67 | api-inference.modelscope.cn/v1 |
| Deep Infra | 68 | api.deepinfra.com/v1 |
| Z.AI 智谱国际 | 69 | api.z.ai/api/paas/v4 |
| Moonshot AI 国际 | 70 | api.moonshot.ai/v1 |
| Vultr | 71 | api.vultrinference.com/v1 |
| Agnes | 72 | apihub.agnes-ai.com/v1 |

**模型建议**：编辑渠道页新增"填入推荐模型"按钮，一键填入该渠道的最新常用模型（数据来自 models.dev）。

> 已有自定义渠道（type 50）可手动升级为对应内置类型，base_url 不变即可无缝切换。
