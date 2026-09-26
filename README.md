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
    <img src="https://img.shields.io/github/v/release/Dytchem/one-api?color=brightgreen" alt="release">
  </a>
  <a href="https://github.com/Dytchem/one-api/releases/latest">
    <img src="https://img.shields.io/github/release-date/Dytchem/one-api?color=brightgreen" alt="release date">
  </a>
  <a href="https://github.com/Dytchem/one-api/actions/workflows/docker-image.yml">
    <img src="https://img.shields.io/github/actions/workflow/status/Dytchem/one-api/docker-image.yml?color=brightgreen" alt="build status">
  </a>
  <a href="https://github.com/Dytchem/one-api/stargazers">
    <img src="https://img.shields.io/github/stars/Dytchem/one-api?color=brightgreen" alt="stars">
  </a>
  <a href="https://github.com/Dytchem/one-api/blob/main/LICENSE">
    <img src="https://img.shields.io/github/license/Dytchem/one-api?color=brightgreen" alt="license">
  </a>
  <a href="https://github.com/Dytchem/one-api/blob/main/go.mod">
    <img src="https://img.shields.io/badge/Go-1.22-00ADD8" alt="go version">
  </a>
  <a href="https://github.com/Dytchem/one-api/pkgs/container/one-api">
    <img src="https://img.shields.io/badge/镜像-GHCR%20latest-2496ED" alt="image">
  </a>
  <a href="https://github.com/Dytchem/one-api">
    <img src="https://img.shields.io/badge/平台-Linux%20%7C%20Windows%20%7C%20macOS-blue" alt="platform">
  </a>
</p>

---

## 截图

| Chat | Agent |
| :---: | :---: |
| <img src="docs/screenshots/chat.png" width="420" alt="Chat 页面"> | <img src="docs/screenshots/agent.png" width="420" alt="Agent 页面"> |

| 渠道管理 | 日志 |
| :---: | :---: |
| <img src="docs/screenshots/channel.png" width="420" alt="渠道管理"> | <img src="docs/screenshots/log.png" width="420" alt="日志"> |

| 失败日志 | 失败详情 |
| :---: | :---: |
| <img src="docs/screenshots/fail-logs.png" width="420" alt="失败日志"> | <img src="docs/screenshots/fail-logs-single-detail.png" width="420" alt="失败详情"> |

## 这是什么

一个 AI 网关服务：把各类模型渠道（DeepSeek、MiMo、Gemini、Agnes、OpenCode 等）统一接入，用一套令牌对外提供 **OpenAI 兼容接口**，并提供网页 Chat 与 AI Agent。

Fork 自 [songquanpeng/one-api](https://github.com/songquanpeng/one-api)，在完整保留原网关能力的基础上深度定制：**内置 Chat 与 Agent、渠道与探测机制完善、日志系统重构、6 轮安全审计收敛、性能优化、跨设备会话同步、CI/CD 发布流水线**。

## 主要变更（自 fork 以来）

### 内置 Chat 与 AI Agent（本项目最大特色）

- **内置 Chat**：网页聊天，多模态附件（图片/音频/视频）、思考等级、指定渠道与令牌
- **内置 AI Agent**：工具调用直接操作 one-api——查询/测试/管理渠道、用户、令牌、日志
- **后台执行架构**：请求在 pi-bridge 后台运行，UI 只是订阅者——**刷新/离开页面不中断生成，回来自动续传（resume）**
- **跨设备会话同步**：登录用户会话按账号云端共享，任意设备登录均见同一份记录（含历史消息），本地优先合并、不影响续传
- **Agent 工具全量覆盖管理 API**：渠道（增/改/删/复制/测试/余额/排序/模型探测）、用户、令牌、日志、系统状态
- **网络搜索与网页抓取**（AnySearch，匿名）：Web Search / Web Extract 工具
- **Markdown 数学公式渲染**（KaTeX + markdown-it + texmath，支持 `$...$` / `$$...$$` / `\(...\)` / `\[...\]` 全部语法，公式内中文正常渲染，根号等 SVG 完整保留）
- 工具端点一致性：`test_channel` / `update_channel_balance` 等跟随后端 POST 语义

### 渠道与模型

- 新增 12+ 常用提供商渠道模板与模型建议
- 支持 OpenAI Responses API（含工具调用流式归并）
- 模型映射可视化编辑、一键拉取模型列表、渠道复制
- 渠道健康指标 + **熔断器**（滑动窗口成功率/速度/首 token 延迟，失败自动降级）
- 探测机制完善：tool_calls 流不误判失败、SSE 首 token 超时可配（`PROBE_TIMEOUT`）、keep-alive 注释不误判、空响应自动重试、失败自动禁用（可关）
- **健康路由加权随机**：健康度前 k 渠道按 score×weight 分配（Weight 真正生效）
- **OpenCode Zen / Go 请求头自动补充（v104）**：转发时自动注入上游必需的 `x-opencode-session` 与专用 `User-Agent`。非 opencode 原生客户端（OpenAI SDK / curl / Pi agent 等）直调 `opencode.ai/zen/go/v1` 会因缺该头被上游拒绝（`400 MissingSessionID`）；本网关按「渠道 × 模型」维护稳定会话 ID（30 分钟窗口，兼顾上游路由与 prompt-cache 亲和），调用方自带该头时尊重原值不覆盖，并以渠道类型 + `base_url` 双重识别（`OpenAICompatible` 直填 `opencode.ai` 域名同样生效）

### 日志系统

- 日志详情显示真实内容（探测显示请求、消费显示回复）
- 失败请求/响应完整保留（`log_payloads`）+ UI 失败日志页 + 流异常检测
- `log_payloads` 自动清理（默认 7 天 TTL，`LOG_PAYLOAD_TTL_HOURS` 可配）
- 记录 cache_read / cache_creation tokens（不计费）；用户断开时同步断开上游
- 日志 UI：tok/s 列、两行时间、状态徽章、失败标记列（`is_failed` 索引加速）

### 安全加固（6 轮全库审计收敛）

- **早期加固**：CORS 白名单、SMTP TLS 严格校验、crypto/rand 替换 math/rand、Go 1.22 + gin + sonic + golang-jwt v5 升级、Dockerfile 固定基础镜像、cookie 安全配置（HttpOnly/SameSite）、防邮箱枚举、启动日志去默认密码
- **全库审计（6 轮）**：SSRF 钉 IP 防 rebinding（image_url 抓取加固：超时/限体/禁重定向/私网阻断）、会话伪造与孤儿会话重放防护、bridge 鉴权（`BRIDGE_SECRET` 兼容模式）、验证码爆破防护、审计日志 key 脱敏、GET 副作用改 POST、CSP 收紧、管理员 HTML 清洗、GORM 零值更新修复、数据库弱口令收敛
- **bridge 鉴权（v96–v98）**：`BRIDGE_SECRET` / `AGENT_BRIDGE_SECRET` 共享密钥 + `X-Bridge-Token` 头；**v105 起默认开启**，未配置时自动生成持久化密钥，不再有不校验的兼容模式

### 界面与体验

- **统一画布**：全部设备渲染同一 1440px 画布（iframe 隔离视口），任意端所见一致
- **颜色区分**：渠道按钮（编辑蓝/禁用橙/启用绿）、提供商徽标按品牌色、健康度连续渐变
- **版本宏观变量**：版本号单一来源（根 `VERSION` 文件 → 构建注入 UI / 镜像 tag）
- 全列表分页修复（日志/渠道/令牌/用户 hasMore 推断）+ 失败日志页统一格式
- 手机端布局修复、页面白屏防护

### 稳定性与安全加固（v105 全库审计第二轮）

- **pi-bridge 鉴权默认开启（提权面收敛）**：原实现在 `BRIDGE_SECRET` 未配置时 `requireAuth` 直接放行，而 bridge 信任调用方自报的 `user_id`，同机任意进程都能以管理员身份驱动 Agent 工具。现改为：未显式配置时由 `entrypoint.sh` 自动生成随机密钥（持久化到 `/data/bridge_secret`）并同时注入两侧（**零配置即可启用鉴权**）；bridge 在无密钥且未显式 `BRIDGE_ALLOW_INSECURE=1` 时**拒绝启动**
- **限流器 key 回收修复（内存无界增长）**：原 `clearExpiredItems` 判定对「正在被访问」的 key 恒为假（队列尾部是刚写入的时间戳），活跃 key 永不回收；key 由 `ClientIP` 生成且 CORS 为 `AllowAllOrigins`，公网可铸造任意多 key ⇒ map 无界增长至 OOM。同时修掉 `expirationDuration` 的无锁读（data race）
- **响应体泄漏与无界读取**：`GetResponseBody` 原先只在 200 且读取成功时 `Close()`，非 200 / 读取出错的早退路径泄漏 fd（余额刷新为定时循环，渠道抖动时会耗尽 fd）；连同渠道测试、Agent 桥接响应一并限制读取上限
- **请求体改写后同步 `ContentLength`**：`extractChannelId` 重写 body 却未更新长度，下游按旧长度读取（音频路径会原样转发给 Azure）导致上游截断/挂起
- **审计日志限读**：`AuditLog` 原无上限 `io.ReadAll` 请求体（`string`+正则+`[]rune` 峰值 3-4 倍），现只读前 64KB，未读部分经 `MultiReader` 拼回，**下游仍拿到完整 body**
- **bridge 进程级兜底**：补 `unhandledRejection` / `uncaughtException` 处理——`readBody` 在客户端中途断开时以 `ECONNRESET` 拒绝且调用点在 try 之外，Node 默认会终止进程，单个 abort 请求即可打挂整个 Chat/Agent 子系统
- **entrypoint 就绪检查闭环**：原 20 次循环后无条件继续（bridge 启动失败时外部只见 502、无任何信号），现失败即打印日志并显式告警；日志移出未挂卷的 `/tmp`
- **渠道分发防御性判空**：`channel.Id` 解引用前加显式守卫（两条查询路径都不会返回 `(nil, nil)`，但真出现时会把请求打成 panic）

### 缓存与侦测加固（v108）

- **无效令牌负缓存**：`CacheGetTokenByKey` 原先查库失败直接返回错误、**不缓存失败结果**，于是任何随机 `sk-` 键的请求都会打到数据库（找不到也要查一次库），公网可无上限铸造随机键即构成 DB 放大面。现对 `gorm.ErrRecordNotFound`（**且仅**该情形——连接失败/超时等不缓存，避免把数据库抖动固化成假"令牌无效"）做 30s 负缓存；令牌增删改路径连带清除负缓存，保证新建令牌立即可用。v106 的 `/v1` 限流是兜底，这条才是让缓存真正生效的正解
- **entrypoint 就绪探测修复**：原探测用 `curl`，而最终镜像 `node:20-alpine` 只装了 `ca-certificates tzdata`、**没有 curl**，导致每次启动都误报"pi-bridge 未就绪"（bridge 其实秒起健康）——这种必然失败的检查比不检查更糟，会把真正的启动失败淹没在噪声里。现按镜像内实际可用工具退化探测（busybox `wget` → `node` 原生 http → `nc`），并把"无工具可用"与"探测失败"区分开

### 越权与限流加固（v106 / v107）

- **proxy 目标白名单把 `URL.Path` 当成 `target` 用（真实存在、影响可用性）**：非 admin 的 proxy 白名单判定用的是 `c.Request.URL.Path`，而它形如 `/v1/oneapi/proxy/5/v1/chat/completions`（**含路由前缀**），白名单项全是 `/v1/...` → `HasPrefix` 对任何输入**恒为 false**，即普通用户的 proxy 请求从未被放行过（proxy 对非 admin 完全不可用）。v107 改用 `c.Param("target")`（通配段捕获值），并在匹配前 `path.Clean`，使合法端点恢复可用、`..` 逃逸与 `/v1` 外目标仍被拒
  > 说明：v106 曾把"非管理员可经 `..` 触达上游任意路径"记为可越权漏洞。经真实 gin 实测复核，该路径因上述恒 false 而**本就被 403**，**并不可达**；真正的缺陷是白名单判错对象导致功能不可用。此处据实更正
- **proxy 适配层命名空间约束**：`GetRequestURL` 解析并 `path.Clean` 目标路径后强制要求落在 `/v1/` 内；并显式断言渠道前缀确实被剥离（重试换渠道时 `RequestURLPath` 仍是首次请求的 URL，`TrimPrefix` 会变成 no-op 而把前缀整体透传给上游）
- **`/v1` 与 `/v1/models` 转发路径限流**：原先这两处只有 `TokenAuth`、完全没有限流，而 token 未命中缓存时每个请求都查一次库（token **无负缓存**）——用随机 `sk-` 键刷即可无上限压数据库。新增独立的 `RelayRateLimit`（`RELAY_RATE_LIMIT`，默认 3000/3min，置于 `TokenAuth` 之前以在查库前生效；设 0 关闭），并同时挂到 `/v1/models` 组；与 `/api` 配额互不挤占，默认值宽松以免误伤流式长连接。⚠️ 限流按 `ClientIP` 计，若前置反代**未**透传 `X-Forwarded-For`，所有用户会共用同一配额桶
- **bridge 密钥持久化**：自动生成的 `BRIDGE_SECRET` 现持久化到 `/data/bridge_secret`（0600，与 `session_secret` 同卷），避免每次容器重启换新密钥导致 bridge 与 one-api 两侧 desync、Chat/Agent 静默失效

### 性能（v100 性能大更新）

- 每请求 DB 往返 ~11 次 → **~4 次**
- 进程内 TTL 缓存（token / 用户分组 / 状态，Redis 关闭也生效，变更处主动失效）
- 健康分原子快照（O(1) 无锁读）+ 健康路由复用分数 + 内存渠道快照
- SSE 每行 JSON 只解析一次；连接池默认贴合 MySQL `max_connections`
- 消费日志批量写（100ms 合并 INSERT + 用户名一次 IN 回填）；日志表三合一索引
- pi-bridge：脏会话标记 + 异步落盘（原子替换）+ SSE 头先发（模型同步不阻塞首字节）

## 快速开始

```bash
# 最简启动（host 网络 + MySQL + 代理），Chat/Agent bridge 默认端口 3005
docker run -d --name one-api --restart unless-stopped --network host \
  -v /data:/data \
  -e SQL_DSN="user:password@tcp(127.0.0.1:3306)/one-api?charset=utf8mb4&parseTime=True&loc=Local" \
  -e PORT=3004 \
  -e ONEAPI_BASE="http://127.0.0.1:3004" \
  -e HTTP_PROXY="http://127.0.0.1:8118" \
  -e HTTPS_PROXY="http://127.0.0.1:8118" \
  -e NO_PROXY="localhost,127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16" \
  ghcr.io/dytchem/one-api:latest
```

访问 `http://127.0.0.1:3004`，初始管理员账号 `root`（密码见容器启动日志）。

> 完整镜像列表见 [ghcr.io/dytchem/one-api](https://github.com/Dytchem/one-api/pkgs/container/one-api)（`latest` / `main` / 全部 `v*` 版本 tag；发布附带 Linux amd64/arm64、Windows、macOS 单文件二进制）。

### 部署说明

- `PORT` 非默认（3000）时必须设置 `ONEAPI_BASE` 同值；3005 被占时加 `AGENT_BRIDGE_URL` / `BRIDGE_PORT`
- **无需任何部署级 key**：模型同步与工具凭据都用登录用户自己的令牌（前端自动选用当前账号第一个可用令牌）
- **bridge 鉴权默认开启**：未配置 `BRIDGE_SECRET` 时由 `entrypoint.sh` 自动生成并持久化到 `/data/bridge_secret`，同时注入 bridge 与 one-api（零配置即启用）；跨容器/跨机部署才需显式成对配置两侧同值。仅本机调试可用 `BRIDGE_ALLOW_INSECURE=1` 显式关闭（bridge 在无密钥且未显式放开时**拒绝启动**）
- 代理仅用于出站（上游模型 / 搜索抓取），`NO_PROXY` 建议覆盖内网与自有域名

### 环境变量速查

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `SQL_DSN` | — | MySQL 连接串（必填） |
| `PORT` | 3000 | 网关端口 |
| `ONEAPI_BASE` | — | 网关外部访问地址（`PORT` 非默认时必填） |
| `AGENT_BRIDGE_URL` / `BRIDGE_PORT` | 3005 | Chat/Agent bridge 地址 / 端口 |
| `BRIDGE_SECRET` / `AGENT_BRIDGE_SECRET` | 自动 | bridge 鉴权密钥（自动生成并持久化到 `/data/bridge_secret`，0600；跨容器部署需两侧显式同值） |
| `RELAY_RATE_LIMIT` | 3000 | `/v1` 与 `/v1/models` 转发限流次数（固定 3 分钟窗口，按 ClientIP 计；0 关闭） |
| `PROBE_TIMEOUT` | 120s | 渠道探测 SSE 首 token 超时 |
| `LOG_PAYLOAD_TTL_HOURS` | 168 | 失败日志 payload 保留时长 |
| `SESSION_SECRET` | 自动 | 会话密钥（自动生成持久化，0600） |

## 文档与支持

- [API 文档](./docs/API.md)
- 变更历史：见 [Releases](https://github.com/Dytchem/one-api/releases)（v100 为自 fork 以来全量变更总览）
- 上游项目：[songquanpeng/one-api](https://github.com/songquanpeng/one-api)

## License

[MIT](./LICENSE)
