# Kiro-Go 15 分钟持续监控与 Claude Code API 影响排查

日期：2026-05-21 10:32:47 至 10:47:03 Asia/Shanghai  
服务：`/www/Kiro-Go` Docker Compose `kiro-go`  
地址：`http://127.0.0.1:8080`  
验证方式：Docker 健康检查、API 采样、JSON 持久化摘要、后端日志、Playwright-MCP 真实浏览器截图与控制台、开源项目对照研究。

## 结论

Verdict: **SERVICE HEALTH PASS / OPUS 4.7 CLAUDE CODE GENERATION BLOCKED**

不能标记为完整 PASS。Kiro-Go 进程、Docker、管理后台、基础 API、模型列表、`count_tokens` 均正常；但 Opus 4.7 fleet 在 15 分钟窗口内持续 `blocked`，`safeConcurrency=0`，`locallySchedulableAccounts=0`，`circuitState=half_open`，`lastPressureReason=rate_limited_or_model_capacity`。这会直接影响 Claude Code 使用 Opus 4.7 调 `/v1/messages` 时的体验：请求可能长时间排队/保活等待真实上游内容，看起来像“7 分钟无响应”。

## 15 分钟监控摘要

采样文件：`api/sample-01` 至 `api/sample-15`，共 15 次。

```json
{
  "sampleCount": 15,
  "window": {
    "start": "2026-05-21T10:32:47+08:00",
    "end": "2026-05-21T10:47:03+08:00"
  },
  "healthStatuses": [{"status": "ok", "count": 15}],
  "fleetStatuses": [{"status": "blocked", "count": 15}],
  "safeConcurrency": {"min": 0, "max": 0},
  "circuitStates": [{"state": "half_open", "count": 15}],
  "localSchedulable": {"min": 0, "max": 0},
  "pressureReasons": [{"reason": "rate_limited_or_model_capacity", "count": 15}],
  "requestCounters": {
    "first": {"total": 16094, "success": 6704, "failed": 9390},
    "last": {"total": 16094, "success": 6704, "failed": 9390}
  }
}
```

请求计数在监控窗口内未增长，说明本次 15 分钟监控未制造额外生成负载。

## Docker 与基础 API

PASS：容器持续 healthy，`/health` 15 次均返回 `status=ok`。最终 Docker health `Status=healthy`、`FailingStreak=0`。资源占用平稳，采样窗口 CPU 大多为 `0.00%`，峰值 `1.76%`；内存约 `9.48MiB` 至 `10.79MiB / 15.62GiB`。

PASS：轻量接口正常。

- `GET /v1/models`: HTTP 200，约 0.006s，返回模型列表。
- `POST /v1/messages/count_tokens`: HTTP 200，约 0.001s，返回本地估算 `input_tokens`。
- 未带 API key 的受保护请求返回 HTTP 401，属于预期鉴权行为，不是故障。

## 持久化/数据库证据

本项目使用 `data/config.json` 作为自身持久化状态，不是 SQL 数据库。监控只记录脱敏摘要，未输出 token、密码或 API key。

首末样本摘要：

- Accounts: 27
- Enabled accounts: 27
- Cooling accounts: 0
- Temporary limited in persisted summary: 0
- `config_mtime`: `10:32:30` -> `10:46:55`
- 配置启用了 `requireApiKey=true`、`stableDownstream`、`contentContinuity`，Opus 4.7 内容连续性最大等待为 120 秒。

注意：持久化摘要没有记录 temporary-limited，但运行时日志和 fleet runtime health 显示上游压力。这符合当前实现：模型容量压力与账号健康分开追踪，不能只看配置文件判断是否可用。

## 后端日志

本窗口未发现 `panic`、`fatal`、崩溃、健康检查失败或 `upstream first event timeout`。

发现的关键 warning：

- 多次 Kiro IDE 返回 account temporary-limit 429，消息包含 suspicious activity / temporary limits。
- 每 5 分钟出现：`[HealthCheck] Skipped model probes while Opus 4.7 quiet mode is active`。
- 每 5 分钟出现：`[AutoRefresh] Skipped expensive refresh while Opus 4.7 quiet mode is active`。
- 每 5 分钟出现：`[ModelsCache] Skipped refresh while Opus 4.7 quiet mode is active`。

解释：quiet mode 是保护机制，避免在 Opus 4.7 上游压力期间继续用昂贵探测放大限流；它会降低刷新/探测积极性，所以短时间内 fleet 不会自动恢复到健康状态。

## Opus 4.7 Fleet 证据

最终样本 `api/sample-15/fleet.json`：

```json
{
  "status": "blocked",
  "circuitState": "half_open",
  "safeConcurrency": 0,
  "locallySchedulableAccounts": 0,
  "lastPressureReason": "rate_limited_or_model_capacity",
  "recommendedQueueWaitSeconds": 120,
  "currentInFlight": 1,
  "contentSuccessRate": 1,
  "recentContentRequests": 0,
  "recentEmptyCompletions": 0,
  "recentStableFallbacks": 0,
  "summary": {
    "eligible": 25,
    "enabled": 27,
    "total": 27,
    "quotaBlocked": 0,
    "coolingDown": 0
  }
}
```

截图 `screenshots/kiro-monitor-api-final.png` 与 API 证据一致：页面显示 `Status: blocked`、`Circuit: half_open`、`Safe concurrency: 0`、`Schedulable: 0`、`Last pressure: rate_limited_or_model_capacity`，并给出 `sub2api should not send new Opus 4.7 calls until retryAfterSeconds or schedulable capacity recovers`。截图结果正确，应判定 Opus 4.7 生成能力未通过。

## Playwright-MCP 前端验证

PASS：真实浏览器已打开管理后台 API 页面，页面非空，统计与 API 状态一致，Opus 4.7 fleet 卡片准确显示 blocked 状态。控制台采集结果：Errors 0、Warnings 0。

截图：

- `screenshots/kiro-monitor-api-mid.png`
- `screenshots/kiro-monitor-api-final.png`

## 对 Claude Code “7 分钟无响应”的影响判断

根因判断：**不是 Kiro-Go 进程崩溃，也不是前端/数据库异常；主要是 Opus 4.7 上游 temporary-limit/model-capacity 压力叠加本地内容连续性等待策略。**

当前配置对 Claude Code / Anthropic 请求的策略是：在 Opus 4.7 可重试容量压力下继续排队，流式请求发送 ping 心跳，但不提前返回 fallback assistant 内容。这个策略避免了“HTTP 200 但空内容/假成功”，但在 fleet 全程 `safeConcurrency=0` 时，Claude Code 侧就会表现为长时间等待。

本次窗口里没有捕获新的 `/v1/messages` 生成请求；最近的 Opus 4.7 生成日志发生在监控前，能成功但延迟高，存在多次重试：约 6.3s、11.9s、13.2s、15.6s、23.1s、36.9s，部分 first token 约 5.2s 至 9.4s，并出现 `model_capacity` / `temporary_limited` attempt trace。这说明系统在压力未完全阻断时可拿到内容；但在 10:32-10:47 的阻断窗口，新 Opus 4.7 流量应停止或排队。

## 开源项目与外部资料对照

- `jwadow/kiro-gateway` README 也定位为 Kiro API 到 Claude/OpenAI 兼容工具的代理，并强调多账号、retry、token refresh、流式支持。
- `jwadow/kiro-gateway` issue #115 公开记录了 Kiro 风控非常严格、账号被 security precaution 临时锁定/暂停，怀疑请求模式触发风控。这与本机日志里的 suspicious activity temporary limits 一致。
- `jwadow/kiro-gateway` issue #153/#158 记录了 Claude Code 卡住/写文件失败的另一类问题：工具调用流片段损坏、thinking tags 插入 tool_result、Kiro 工具输出流约 9KB 截断。这类问题和本次 15 分钟主因不同，但说明 Claude Code 经代理使用 Kiro 时还要关注工具流兼容性。
- `zeoak9297/KiroSwitchManager` README 说明其也是 Kiro 多账号管理与 Claude/OpenAI API 本地代理，包含自动换号、端点切换、消息截断保护、流式心跳保活、一账号一机器码等能力。这支持本次运维建议：避免高频切换机器码/账号，减少触发风控的请求模式。
- Anthropic/Kiro 兼容链路的一般经验也支持：429/overload/capacity、代理超时、stream 心跳、beta/header/工具流兼容都会影响 Claude Code 体感。结合本机证据，本次首要问题是 Opus 4.7 capacity/temporary-limit，而不是协议基础面失败。

## 隐藏 BUG 风险清单

- HIGH：Opus 4.7 全窗口 blocked，Claude Code 使用 Opus 4.7 生成时会继续等待或队列拥塞，用户体感为无响应。
- MEDIUM：`claudeCodeGovernor.enabled=false`，虽然请求日志出现过 governor 标记，但配置层未启用新 governor；多 subagent/后台任务并发时仍可能抢占有限 Opus 4.7 入口。
- MEDIUM：quiet mode 会跳过昂贵刷新/模型探测，能保护账号但也会让恢复确认变慢；需要依赖 fleet readiness 而非盲目重试。
- MEDIUM：Kiro 上游 temporary-limit suspicious activity 是账号/请求模式风险，不是本地代码能完全修复的 bug；继续高并发压测会恶化。
- LOW：Docker Compose `version` 字段 obsolete，只是噪音 warning，不影响运行。

## 建议处置

1. Claude Code 暂时不要指定 `claude-opus-4.7`；切到 `claude-sonnet-4.5`、`claude-haiku-4.5` 或其他 fleet readiness healthy 的模型。
2. 下游调用前读取 `GET /admin/api/fleet/readiness?model=claude-opus-4-7`；只有 `status=healthy` 且 `safeConcurrency>0` 才发送 Opus 4.7 新请求。
3. 保留当前 quiet mode，不要用高频健康探测刺激上游账号。
4. 如要继续修复 Claude Code 7 分钟无响应，下一步应优先启用/验证 Claude Code governor，限制同 session subagent/background 并发，并在 blocked 时给调用端明确可观测状态。
5. 若要完整 PASS，需要等 Opus 4.7 readiness 恢复 healthy 后，再跑一次真实 `/v1/messages` Claude Code 流式生成 UAT，并验证 screenshots/API/request logs 三方一致。

## 证据文件

- `monitor-window.json`
- `api/samples.jsonl`
- `api/sample-*/summary.json`
- `api/sample-15/fleet.json`
- `api/models-mid.txt`
- `api/count-tokens-opus-mid.txt`
- `logs/kiro-go-monitor-window.log`
- `logs/kiro-go-last-5m.log`
- `screenshots/kiro-monitor-api-mid.png`
- `screenshots/kiro-monitor-api-final.png`

## 验证命令结果

- `go test ./proxy ./pool ./config`: PASS
- Playwright-MCP console: Errors 0 / Warnings 0
- Docker health final: healthy
