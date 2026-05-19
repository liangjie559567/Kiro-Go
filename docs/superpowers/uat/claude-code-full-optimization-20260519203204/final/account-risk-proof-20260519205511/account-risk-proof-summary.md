# Account Risk Proof

## Evidence

- Same account model-list endpoint returned HTTP 200; see `account-models.headers` and `account-models.json`.
- Same account generation test returned `temporary_limited` with upstream suspicious-activity message; see `account-test.headers` and `account-test.json`.
- All configured accounts share the same `userId` prefix count: `{"d-9067c98495":21}`.
- Container config also shows 21 accounts share one `profileArn` value, captured in command output during investigation.
- Cooling accounts after proof: `[{"email":"robyjoao97@gmail.com","userId":"d-9067c98495.f4c824a8-e061-7002-dd6e-1ffa95762572","lastFailureReason":"temporary_limited","remaining":41}]`.

## Conclusion

This is not a token/list-model/network failure. It is an upstream Kiro generation/agentic-request temporary limit applied to the shared Kiro/Amazon Q risk subject. Local `schedulable` means Kiro-Go can route the account; it does not prove upstream generation is allowed.