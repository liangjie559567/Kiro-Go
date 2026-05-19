# Full Claude Code Parity And Real UAT Report

## Verdict

PARTIAL

The core backend/API/database path passed, but the final verdict is PARTIAL because screenshot analysis found that  does not actually show the request-log table. This report does not mark that visual surface as PASS.

## Code Verification

- ?   	kiro-go/logger	[no test files]
ok  	kiro-go	0.007s
ok  	kiro-go/auth	0.003s
ok  	kiro-go/config	0.017s
ok  	kiro-go/pool	0.006s
ok  	kiro-go/proxy	1.487s: PASS. See .

## Docker Health

- Docker service state: PASS. See  and .
- Kiro-Go health: PASS. See .
- sub2api health: PASS. See .

## API Evidence

- sub2api non-stream through Kiro-Go Opus 4.7: PASS. See  and .
- sub2api stream through Kiro-Go Opus 4.7: PASS. See  and .
- sub2api Sonnet 4.5 alias attempt: FAIL_EXPECTED_ALIAS_NOT_SCHEDULABLE. See ; sub2api returned 503 no available accounts before reaching Kiro-Go.
- Kiro-Go readiness: see .
- Kiro-Go model readiness: see .
- Kiro-Go request logs: see .

## Database Evidence

- Database evidence: PASS.
- Before:  and .
- After: , which includes recent  rows for API key 2 and account 24.

## Browser Evidence

- Playwright screenshots: PARTIAL.
- Kiro-Go screenshots: , , , .
- sub2api screenshots: , , , .
- Console/page errors: final  records zero console messages, zero page errors, and zero failed requests.
- Screenshot analysis: .

## Capability Matrix

| Capability | Verdict | Evidence |
| --- | --- | --- |
| Messages API | PASS | ,  |
| Streaming Messages API | PASS | ,  |
| Count tokens | PARTIAL | , ; Kiro-Go discloses estimated counts |
| max_tokens=0 | PARTIAL | , ; local zero-output compatibility is not proven upstream cache warmup |
| Assistant text prefill | PARTIAL | , ; text prefill is converted, tool-use prefill remains unsupported |
| Tool schema validation | PASS | ,  |
| Fine-grained streaming truthfulness | PARTIAL | ; accepted headers do not prove true upstream partial JSON parity |
| Tool reference | PASS | ,  |
| Model readiness | PASS | ,  |
| sub2api downstream non-stream | PASS | ,  |
| sub2api downstream stream | PASS | ,  |
| sub2api Sonnet 4.5 alias scheduling | FAIL | ; no available accounts in sub2api scheduler |
| Kiro-Go request-log visual screenshot | PARTIAL |  does not visibly show the log table; JSON evidence exists in  |

## Residual Risks

- The sub2api  alias is not schedulable in the current downstream scheduler, even though Kiro-Go itself can map that alias. Use a sub2api-listed model such as , or update sub2api model scheduling/mapping separately.
- Browser evidence for Kiro-Go request logs is partial because the screenshot stayed on the Claude Code readiness/model area. API evidence still proves request logs exist and include the real successful calls.
- Several official Anthropic capabilities are intentionally PARTIAL because Kiro upstream cannot be proven to provide exact official semantics, especially true cache warmup and fine-grained partial JSON streaming.
