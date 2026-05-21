# Open Source Research Notes

Date: 2026-05-21

## jwadow/kiro-gateway

Sources:

- https://github.com/jwadow/kiro-gateway
- https://github.com/jwadow/kiro-gateway/blob/main/README.md
- https://github.com/jwadow/kiro-gateway/issues/115
- https://github.com/jwadow/kiro-gateway/issues/153

Findings:

- Supports outbound proxy through `VPN_PROXY_URL`, including HTTP, HTTPS, SOCKS5, and authenticated proxy URLs.
- Multi-account support uses account management, recoverable error handling, and account switching.
- Streaming retry boundary is first-token oriented; after downstream stream content starts, replay is unsafe.
- Issue evidence does not prove a fixed IP or dynamic IP root cause. It shows user reports of temporary account limits and maintainers asking for environment, proxy, and usage details.
- Useful pattern for Kiro-Go: keep retry budgets bounded and avoid retry amplification with Claude Code/sub2api.

Limitations:

- This project does not prove dynamic proxy IP fixes `INSUFFICIENT_MODEL_CAPACITY`.
- It does not prove temporary account limits are purely IP based.
- Its AGPL-3.0 license means implementation ideas can be studied, but code should not be copied directly into Kiro-Go without accepting license implications.

## zeoak9297/KiroSwitchManager

Sources:

- https://github.com/zeoak9297/KiroSwitchManager
- https://github.com/zeoak9297/KiroSwitchManager/blob/main/README.md
- https://github.com/zeoak9297/KiroSwitchManager/releases/tag/v2.5.0
- https://github.com/zeoak9297/KiroSwitchManager/releases/tag/v2.7.0

Findings:

- Public repository provides README and release artifacts, but no source code.
- README/release notes claim a local reverse proxy, Claude/OpenAI compatible APIs, stream heartbeat keepalive, WebSearch injection, account switching, and Kiro CLI/IDE modes.
- It is useful as a feature checklist, not as implementation-level proof.

Limitations:

- No public source means no verifiable algorithm for account switching, proxy rotation, cooldowns, retry classification, or streaming heartbeats.
- It cannot prove dynamic IP is required or sufficient for Opus 4.7 pressure.

## IP / Dynamic Proxy Conclusion

Current local evidence is mixed:

- `INSUFFICIENT_MODEL_CAPACITY` is model capacity pressure. Dynamic IP cannot solve this.
- `temporary limits` / `suspicious activity` can involve account state, request frequency, behavior pattern, and possibly outbound IP reputation.
- `Too many requests` can be account, model, endpoint, or exit-path related.

Therefore dynamic proxy IP may help only the exit-IP reputation part of temporary limits. It is not a complete fix and cannot make 100/100 Opus 4.7 pass while upstream capacity is unavailable.

Kiro-Go already supports static global and per-account proxy configuration. This UAT did not validate dynamic proxy rotation because no trusted proxy pool endpoints were provided.
