# 使用 API 操控 & 扩展 One API
> 欢迎提交 PR 在此放上你的拓展项目。

例如，虽然 One API 本身没有直接支持支付，但是你可以通过系统扩展的 API 来实现支付功能。

又或者你想自定义渠道管理策略，也可以通过 API 来实现渠道的禁用与启用。

## 鉴权
One API 支持两种鉴权方式：Cookie 和 Token，对于 Token，参照下图获取：

![image](https://github.com/songquanpeng/songquanpeng.github.io/assets/39998050/c15281a7-83ed-47cb-a1f6-913cb6bf4a7c)

之后，将 Token 作为请求头的 Authorization 字段的值即可，例如下面使用 Token 调用测试渠道的 API：
![image](https://github.com/songquanpeng/songquanpeng.github.io/assets/39998050/1273b7ae-cb60-4c0d-93a6-b1cbc039c4f8)

## 请求格式与响应格式
One API 使用 JSON 格式进行请求和响应。

对于响应体，一般格式如下：
```json
{
  "message": "请求信息",
  "success": true,
  "data": {}
}
```

## API 列表
> 当前 API 列表不全，请自行通过浏览器抓取前端请求

如果现有的 API 没有办法满足你的需求，欢迎提交 issue 讨论。

### 获取当前登录用户信息
**GET** `/api/user/self`

### 为给定用户充值额度
**POST** `/api/topup`
```json
{
  "user_id": 1,
  "quota": 100000,
  "remark": "充值 100000 额度"
}
```

## 转发协议入口

网关提供多种协议的**原生入口**，同一个令牌即可使用。客户端用哪种协议请求，就用哪种格式得到响应（网关内部统一转成 chat 格式转发，出口再按渠道类型转换）。

### OpenAI 兼容（默认）
| 端点 | 说明 |
| --- | --- |
| **POST** `/v1/chat/completions` | 对话补全（标准入口） |
| **POST** `/v1/responses` | Responses API（新项目推荐；助手/工具场景） |
| **POST** `/v1/completions` | 旧版补全 |
| **POST** `/v1/embeddings` | 向量 |
| **POST** `/v1/images/generations` | 图像生成 |
| **POST** `/v1/audio/{speech,transcriptions,translations}` | 语音 |
| **POST** `/v1/moderations` | 内容审核 |
| **GET** `/v1/models` | 模型列表 |

### Anthropic Messages 原生入口
**POST** `/v1/messages`

让 Claude Code / Anthropic SDK 等原生 Anthropic 客户端直接接入。请求与响应均为 Anthropic 格式：

```bash
curl https://oneapi.dytchem.cn/v1/messages \
  -H "Authorization: Bearer sk-xxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-5",
    "max_tokens": 1024,
    "system": "You are concise.",
    "messages": [{"role": "user", "content": "hello"}]
  }'
```

支持的映射：`system`（字符串或 block 数组）、`tool_use` ↔ `tool_calls`、`tool_result` → `role=tool`、base64/URL 图片、`stop_sequences`、`tool_choice`（`auto`/`any`/`tool`/`none`）。流式响应会输出完整 Anthropic SSE 事件序列（`message_start` → `content_block_*` → `message_delta` → `message_stop`）。

> 该入口的**渠道选择与 OpenAI 入口完全一致**——Anthropic 客户端也可以路由到任意 OpenAI 兼容渠道，反之亦然。
>
> `POST /v1/messages/count_tokens` 未实现，返回 501。

### Gemini Interactions 原生入口
**POST** `/v1beta/interactions`

Google 自 2026-06 起将该 API 作为 Gemini 主推入口。

```bash
curl https://oneapi.dytchem.cn/v1beta/interactions \
  -H "Authorization: Bearer sk-xxxx" \
  -H "Content-Type: application/json" \
  -d '{"model": "gemini-3.6-flash", "input": "Tell me a short story."}'
```

`input` 支持纯字符串、`{"type":"user_input"|"model_output","content":[...]}` 对话数组，以及扁平的 `text`/`image` block 数组。

**以下能力网关无法代持，会返回明确错误而非静默降级**：`tools`（`google_search`/`mcp_server`/`computer_use` 等服务端工具）、`background`、`environment`、`previous_interaction_id`（网关无状态，请改为发送完整 `input` 历史）。

## 上游协议（出口）

网关默认以 **OpenAI chat** 协议与上游通信。以下协议可按渠道开启，**默认关闭**，不影响既有渠道：

| 渠道配置 | 上游协议 | 适用 |
| --- | --- | --- |
| `{"use_responses_api": true}` | `POST /v1/responses` | 只提供 Responses 端点的上游 |
| `{"use_interactions_api": true}` | `POST /v1beta/interactions` | Gemini 新版统一入口（替代 generateContent） |

在渠道的「配置」里填上述 JSON 即可。入口协议与出口协议相互独立，可任意组合，例如：客户端用 Anthropic 原生协议进来，网关用 Responses 协议发给上游。

### Anthropic 新特性透传

经网关转发到 Anthropic 渠道时，以下内容会**完整保留**（无需配置）：

- `anthropic-beta` 请求头：客户端声明的 beta 全部保留（支持多次 header 与逗号分隔），网关再补上基础 beta
- `anthropic-version`：按客户端声明透传，缺省 `2023-06-01`
- `thinking`：扩展思考（`{"type":"enabled","budget_tokens":N}` 或 `{"type":"adaptive"}`）
- `context_management`：上下文压缩 / context editing

### 已退役端点
OpenAI **Assistants / Threads API 已于 2026-08-26 全面关停**。`/v1/assistants*` 与 `/v1/threads*` 路径仍保留映射，但返回 **410 Gone** 并提示迁移到 `/v1/responses`（不再返回 501，以免被误读为"尚未实现"）。

> 注意：**Chat Completions 未被废弃**，仍可长期使用；2026-11-30 关停的是 Prompt Objects 与部分旧模型。

## 其他
### 充值链接上的附加参数
One API 会在用户点击充值按钮的时候，将用户的信息和充值信息附加在链接上，例如：
`https://example.com?username=root&user_id=1&transaction_id=4b3eed80-55d5-443f-bd44-fb18c648c837`

你可以通过解析链接上的参数来获取用户信息和充值信息，然后调用 API 来为用户充值。

注意，不是所有主题都支持该功能，欢迎 PR 补齐。