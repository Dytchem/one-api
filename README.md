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
> 本 Fork 偏**自用**，部分功能（尤其是计费相关）可能与原版不一致，请以测试为准。

## 主要改进

### Fallback / 探测
| 改进 | 说明 |
|------|------|
| **Fallback 增强** | 重试时恢复原始模型名，维护失败渠道集合，支持优先级降级 |
| **Stream Probe** | 流式响应空内容探测，避免半途断流；TTFT 先行写入日志 |
| **探测失败记录上游细节** | 日志附 `\| 上游：连接断/异常结束 (HTTP 200, 6行/1887字节, 末行: ...)` 真实断流位置 |
| **empty response 同渠道重试** | 探测空响应时同渠道重试 1 次（dyt-18 改进，dyt-23 修复 body 一致性）|
| **探测 body 完全一致** | 两次 doProbe 用同一份 body bytes，避免 adaptor 转换丢字段触发 2013 |

### 渠道健康度 / 性能
| 改进 | 说明 |
|------|------|
| **渠道健康度** | 滑动窗口追踪 + 连续失败熔断，tok/s 性能指标 |

### 日志系统
| 改进 | 说明 |
|------|------|
| **日志详情统一** | 四类统一格式（探测成功/失败/回复完成/测试完成），全部显示 `请求模型：渠道名/模型名` |
| **类型徽章** | 默认主题新增 成功/失败/完成/测试 四种彩色徽章 |
| **回复内容记录** | 非流式请求日志记录真实 AI 回复（非用户 prompt）|
| **耗时/流式标签** | 详情列独立显示耗时徽章 + Stream/Non-Stream 标识 |
| **末行预览 200 字节** | 流式失败时末行预览从 60→200 字节，含 tool_calls 截断信息 |
| **状态码徽章** | 失败日志加 `[2013:invalid params]` 类状态码徽章 |
| **tools 摘要** | 请求预览附 `[tools×47:agents_list,browser,...]` 摘要 |
| **统一格式** | `模型：jarvis→MiniMax-M3` 格式，所有日志（探测/失败/渠道尝试）一致 |
| **去重** | empty_response 不再产生重复日志（只写探测失败，不写渠道尝试）|

### 失败调试（dyt-20+）
| 改进 | 说明 |
|------|------|
| **payload 完整保留** | 失败请求/响应/错误完整存入 `log_payloads` 表，request 可达 ~700KB |
| **流异常检测** | `finish_reason: tool_calls` 但响应截断，自动判定为流异常 |
| **UI 失败日志浏览器** | `/fail-logs` 路由，按时间倒序列出失败，支持按渠道/模型筛选 |
| **payload 在线查看** | 点击日志行展开完整 JSON（请求+响应+错误），可直接复制 |

### CI/CD
| 改进 | 说明 |
|------|------|
| **构建优化** | 移除 QEMU 多架构、启用 Docker 层缓存，构建时间从 6min→~3min |
| **仅 tag 触发** | 推送 tag 才构建，普通 commit 不触发 |

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

当前最新：**[v0.6.11-dyt-23](https://github.com/Dytchem/one-api/releases/tag/v0.6.11-dyt-23)**

## 失败日志调试工作流（dyt-20+）

1. 进入 `/fail-logs` 路由，按时间倒序看失败列表
2. 失败日志带：
   - 渠道 + 模型（`渠道：MiniMax Frontier(#27)，模型：jarvis→MiniMax-M3`）
   - 上游错误细节（`\| 上游：连接断/异常结束...`）
   - 状态码徽章（`[2013:invalid params]`）
3. 点击行展开 → 看完整 request JSON（最大 700KB）
4. 把完整 JSON 贴给上游厂商工单（含 47 个 tools schema）

## 技术栈

由 **[OpenClaw](https://github.com/openclaw/openclaw)** AI Agent 协助开发维护。

---

*上游项目：[songquanpeng/one-api](https://github.com/songquanpeng/one-api)，MIT 协议。*
