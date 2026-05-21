# Kiro-Go

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat&logo=docker)](https://www.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

将 Kiro 账号转换为 OpenAI / Anthropic 兼容 API 服务，并提供多账号调度、Claude Code 兼容能力和 Web 运维控制台。

[English](README.md) | 中文

如果这个项目帮到了你，欢迎点个 Star 支持一下。

## 支持能力

### 兼容 API 端点

| 能力 | 端点 | 说明 |
|---|---|---|
| Anthropic Messages | `POST /v1/messages` | 也兼容 `/messages`、`/anthropic/v1/messages`；支持流式和非流式。 |
| Anthropic Count Tokens | `POST /v1/messages/count_tokens` | 本地估算，用于 Claude Code 兼容；不是 Anthropic 官方精确计数。 |
| OpenAI Chat Completions | `POST /v1/chat/completions` | 也兼容 `/chat/completions`；支持 tools 和流式输出。 |
| OpenAI Responses | `POST /v1/responses` | 兼容转换层，支持 `instructions`、`input`、function tools、tool output 和 `previous_response_id`。 |
| Models | `GET /v1/models` | 聚合账号模型缓存和内置模型别名。 |
| Stats | `GET /v1/stats` | 需要客户端鉴权的运行统计。 |
| Health | `GET /health` 和 `/` | 容器与服务健康检查。 |
| Claude Code telemetry sink | `POST /api/event_logging/batch` | 直接返回 OK，避免本地网关 telemetry 调用失败。 |

### 网关能力

- 多 Kiro 账号池，默认使用 health 健康优先调度，也支持 `round_robin` 和 `least_connections`。
- 账号运行时健康、模型缓存、配额/订阅信息、临时限制冷却和失败分类。
- 模型映射规则：别名、替换、加权负载均衡目标。
- 高压力模型准入控制，包括 Opus 4.7 并发限制、等待队列、压力评分和 `Retry-After` 提示。
- 面向 sub2api 兼容 Opus 4.7 生成请求的 StableDownstream 模式：下游不暴露 `429`、`502`、`503`；Claude/Anthropic 请求会持续等待真实上游内容，而不是用 fallback 文本结束回合。
- 自动 OAuth token 刷新、定时账号刷新和定时健康检查。
- Claude Code system prompt 过滤、环境噪音过滤、边界标记过滤，以及自定义正则/行级规则。
- 通过模型后缀或 Claude `thinking` 配置启用 thinking 模式，并可配置 Claude/OpenAI 输出格式。
- 全局出站代理和单账号代理。
- 客户端 API Key、多个 client keys、客户端 IP 白名单。
- 请求日志记录账号、路由决策、重试轨迹、工具元数据、延迟、缓存使用、准入压力和 stable fallback 元数据。

### Claude Code 兼容

Kiro-Go 可以作为 Claude Code 的 Anthropic 兼容后端，接收 Claude Code 发出的这些请求形态：

- `tools`、`tool_use`、`tool_result`、`tool_choice`、`tool_reference`
- MCP tool reference、延迟/物化工具、大工具 payload
- 图片内容块
- prompt cache control 和 `max_tokens=0` cache-warmup 形态请求
- fine-grained tool streaming 兼容事件
- 文本 assistant prefill，按续写指令模拟

边界说明：

- Kiro-Go 不启动、不管理本地 MCP server。Claude Code 仍然是 MCP host。
- `count_tokens` 是本地估算。
- `max_tokens=0` 返回本地 zero-output 兼容响应，不代表已证明 Anthropic 官方 cache warmup 等价。
- 文本 assistant prefill 是模拟续写；最终 assistant `tool_use` prefill 会被拒绝。
- fine-grained tool streaming 会输出 Claude Code 兼容事件，但上游是否提供真正的 partial JSON 取决于 Kiro 流式行为。

## 快速开始

### Docker Compose

```bash
git clone https://github.com/liangjie559567/Kiro-Go.git
cd Kiro-Go
mkdir -p data
docker-compose up -d
```

Docker 镜像内置官方 Kiro CLI。可在容器内验证：

```bash
docker compose exec kiro-go kiro-cli --version
```

Kiro CLI 状态保存在 `/app/data/kiro-cli`（使用本 Compose 文件时对应 `./data/kiro-cli`）。不要把本机 Kiro token 烘焙进镜像。

打开管理面板：

```text
http://localhost:8080/admin
```

默认管理密码是 `changeme`。生产环境请使用 `ADMIN_PASSWORD` 覆盖，或在管理面板里修改。

### Docker 运行

```bash
docker run -d \
  --name kiro-go \
  -p 8080:8080 \
  -e ADMIN_PASSWORD=your_secure_password \
  -e CONFIG_PATH=/app/data/config.json \
  -v /path/to/data:/app/data \
  --restart unless-stopped \
  ghcr.io/liangjie559567/kiro-go:latest
```

### 源码编译

```bash
git clone https://github.com/liangjie559567/Kiro-Go.git
cd Kiro-Go
go build -o kiro-go .
ADMIN_PASSWORD=your_secure_password ./kiro-go
```

默认配置文件为 `data/config.json`。Docker 部署时请挂载或备份 `/app/data`。

运行镜像还会安装 `kiro-cli` 和 `kiro` 别名。手动 `docker run` 时如果需要持久化 CLI 状态，请挂载 `/app/data` 并设置 `KIRO_CLI_HOME=/app/data/kiro-cli`。

## 基础使用

先在管理面板添加账号，再调用兼容 API。

### Anthropic Messages

```bash
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -H "Authorization: Bearer any" \
  -d '{
    "model": "claude-sonnet-4.5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "你好！"}]
  }'
```

### OpenAI Chat Completions

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "你好！"}]
  }'
```

### OpenAI Responses

```bash
curl http://localhost:8080/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any" \
  -d '{
    "model": "gpt-4o",
    "instructions": "回答要简洁。",
    "input": "用一句话解释 Kiro-Go。"
  }'
```

## Claude Code 设置

可以先从管理端兼容性接口获取当前推荐环境变量：

```bash
curl http://localhost:8080/admin/api/claude-code/compat \
  -H "X-Admin-Password: your_admin_password"
```

常见本地配置：

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN=any
export ANTHROPIC_API_KEY=any
export ANTHROPIC_MODEL=claude-sonnet-4.5
export ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-4.5
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1
export CLAUDE_CODE_ENABLE_FINE_GRAINED_TOOL_STREAMING=1
export ENABLE_TOOL_SEARCH=true
```

常用 Claude Code 诊断端点：

- `GET /admin/api/claude-code/compat`
- `GET /admin/api/claude-code/readiness`
- `GET /admin/api/claude-code/model-readiness?model=claude-opus-4-7`
- `GET /admin/api/fleet/readiness?model=claude-opus-4-7`
- `GET /admin/api/request-logs`

## Opus 4.7 与 sub2api

Kiro-Go 对 Opus 4.7 做了专门处理，因为它更容易遇到上游容量和临时限制。

- `claude-opus-4-7`、`claude-opus-4.7`、日期后缀和 thinking 后缀会归一到 Kiro 的 Opus 4.7 模型。
- Claude Code 的 Opus 4.7 请求会归一为 adaptive thinking，并丢弃 sampling 参数。
- 模型准入控制会把模型压力和账号健康分开追踪。
- `GET /admin/api/fleet/readiness?model=claude-opus-4-7` 返回 `healthy`、`degraded` 或 `blocked`，以及安全并发和重试时间。
- StableDownstream 只保护 sub2api 下游 HTTP 契约，避免生成链路泄漏网关级 `429`、`502`、`503`。
- 内容连续性单独统计：只有收到真实上游 assistant 文本、thinking 或 tool_use 时才算 `contentSuccess=true`。
- 对 Claude Code / Anthropic 请求，Opus 4.7 可重试容量压力会让 HTTP 请求继续排队，直到拿到真实上游内容或客户端断开。流式请求会在准入、容量和上游首 token 等待期间发送 Anthropic `ping` 心跳，但不会启动 assistant message，也不会为 `attempt_budget_exhausted`、`admission_pressure` 或无账号压力发送 fallback 文本。
- OpenAI 兼容 stable fallback 仍是传输层兜底，会记录为 `contentSuccess=false`，不能算正确模型回复。

当 Kiro-Go 和 sub2api 都可用时，可运行稳定下游 UAT：

```bash
SUB2API_BASE_URL=http://127.0.0.1:18080 \
SUB2API_API_KEY="$SUB2API_API_KEY" \
ROUNDS=10 \
CONCURRENCY=10 \
node docs/superpowers/uat/sub2api-opus47-stable-200/run-stable-200-uat.js
```

## 管理与运维

Web 管理面板和 `/admin/api/*` 支持：

- 账号 CRUD、批量启用/禁用/刷新、账号测试、账号导出。
- IAM Identity Center 登录、Builder ID 登录、SSO token 导入、credentials JSON 导入。
- `POST /admin/api/*` 下的 credentials validate dry-run 凭证校验。
- 单账号或全账号模型缓存刷新。
- 账号 diagnostics、scheduler preview、fleet readiness、WebSearch diagnostics、Claude Code compat/readiness/model-readiness、admission pressure。
- API Key、客户端 IP 白名单、模型映射、模型准入、超额使用、自动刷新、健康检查、负载均衡、端点偏好、代理、thinking、prompt filter 等设置。
- 请求日志、请求统计和统计重置。

Admin API 使用 `X-Admin-Password` 或管理密码 cookie 鉴权。

## 配置

主配置存储在 `data/config.json`。大部分配置也可以在管理面板中修改。

| 配置区域 | 字段 / UI |
|---|---|
| 服务 | `host`、`port`、`password` |
| 客户端访问 | `requireApiKey`、`apiKey`、`clientApiKeys`、`clientIPAllowlist` |
| 模型路由 | `modelMappings`、`loadBalance`、账号 `weight` |
| 准入控制 | `modelAdmission`、兼容旧字段 `opus47Admission` |
| 稳定下游 | `stableDownstream.enabled`、`stableDownstream.sub2apiCompatible`、`stableDownstream.models` |
| 账号 | token、认证方式、区域、machine ID、profile ARN、用量、配额、冷却、代理 |
| 后台任务 | `autoRefresh`、`healthCheck` |
| Thinking | `thinkingSuffix`、`openaiThinkingFormat`、`claudeThinkingFormat` |
| 端点 | `preferredEndpoint`、`endpointFallback` |
| Prompt 过滤 | `filterClaudeCode`、`filterEnvNoise`、`filterStripBoundaries`、`promptFilterRules` |
| 代理 | 全局 `proxyURL` 和账号级 `proxyURL` |
| 日志 | `logLevel` 或 `LOG_LEVEL` |

## 环境变量

| 变量 | 说明 | 默认值 |
|---|---|---|
| `CONFIG_PATH` | 配置文件路径 | `data/config.json` |
| `ADMIN_PASSWORD` | 管理密码覆盖值 | 配置值，首次运行默认为 `changeme` |
| `LOG_LEVEL` | 日志级别：`debug`、`info`、`warn`、`error` | 配置值或 `info` |

## 更多文档

- [Claude Code 兼容性矩阵](docs/claude-code-compatibility-matrix.md)
- [高可用矩阵](docs/kiro-ha-compatibility-matrix.md)
- [Kiro 生态运维说明](docs/kiro-ecosystem-operations.md)
- [sub2api Opus 4.7 Stable 200 UAT](docs/superpowers/uat/sub2api-opus47-stable-200/README.md)

## 参与贡献

欢迎友好交流。遇到问题时，建议先让 Claude Code、Codex 等工具帮忙排查一下。带测试或 UAT 证据的 PR 更容易合并。

## 友情链接

- [LINUX DO](https://linux.do)

## 免责声明

本项目仅供学习和研究目的使用，与 Amazon、AWS 或 Kiro 没有任何关联。用户需自行确保使用行为符合所有适用的服务条款和法律法规，使用风险自负。

## 许可证

[MIT](LICENSE)
