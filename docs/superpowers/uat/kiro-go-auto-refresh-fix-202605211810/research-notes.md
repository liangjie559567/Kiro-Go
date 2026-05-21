# External Research Notes

## jwadow/kiro-gateway

- README describes smart token management with automatic refresh before expiration.
- README documents two refresh paths: Kiro Desktop Auth at `https://prod.{region}.auth.desktop.kiro.dev/refreshToken`, and AWS SSO OIDC at `https://oidc.{region}.amazonaws.com/token`.
- `manual_api_test.py` refresh logic chooses the refresh endpoint by auth type and updates the in-memory Authorization header after successful refresh.
- README and `main.py` document proxy support through `VPN_PROXY_URL`, mapped into `HTTP_PROXY`, `HTTPS_PROXY`, and `ALL_PROXY`, with `NO_PROXY` preserving local access.
- Project guidance says proxy is useful for connection timeouts, corporate network restrictions, and regional connectivity problems. It does not prove that this incident was caused by IP reputation.

## zeoak9297/KiroSwitchManager

- README describes account management, automatic account switching, and scheduled token refresh.
- README says the repository publishes compiled installers only and does not provide source code, so it is product-behavior reference only.

## IP / Dynamic Proxy Conclusion

- Current Kiro-Go evidence does not point to IP as the root cause of the reported auto-refresh failure: a manual UI-triggered refresh succeeded for 24 accounts with 0 failures.
- Dynamic proxy IP should not be introduced as a first fix for this symptom. Keep the existing outbound proxy setting for environments with proven AWS/Kiro connectivity failures, but avoid rotating IPs unless logs show consistent network timeout, regional block, or upstream access denial patterns.
