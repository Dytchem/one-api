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

| 改进 | 说明 |
|------|------|
| **Fallback 增强** | 重试时恢复原始模型名，维护失败渠道集合，支持优先级降级 |
| **Stream Probe** | 流式响应空内容探测，避免半途断流；TTFT 先行写入日志 |
| **渠道健康度** | 滑动窗口追踪 + 连续失败熔断，tok/s 性能指标 |
| **日志详情** | 四类统一格式（探测成功/失败/回复完成/测试完成），全部显示 `请求模型：渠道名/模型名` |
| **类型徽章** | 默认主题新增 成功/失败/完成/测试 四种彩色徽章 |
| **回复内容** | 非流式请求日志记录真实 AI 回复（非用户 prompt） |
| **耗时/流式标签** | 详情列独立显示耗时徽章 + Stream/Non-Stream 标识 |
| **CI/CD** | 移除 QEMU 多架构、启用 Docker 层缓存，构建时间从 6min→~3min |

完整变更记录 → [GitHub Releases](https://github.com/Dytchem/one-api/releases)

## 快速部署

```bash
docker pull ghcr.io/dytchem/one-api:latest
docker run -d --name one-api -p 3000:3000 \
  -e SQL_DSN='user:password@tcp(host:3306)/one-api' \
  ghcr.io/dytchem/one-api:latest
```

> ⚠️ 初次部署后务必修改默认密码 `123456`。

## 版本号约定

```
v0.6.11-dyt-N    # N 为自增构建号，每次发布递增
```

当前最新：**[v0.6.11-dyt-17](https://github.com/Dytchem/one-api/releases/tag/v0.6.11-dyt-17)**

## 技术栈

由 **[OpenClaw](https://github.com/openclaw/openclaw)** AI Agent 协助开发维护。

---

*上游项目：[songquanpeng/one-api](https://github.com/songquanpeng/one-api)，MIT 协议。*
