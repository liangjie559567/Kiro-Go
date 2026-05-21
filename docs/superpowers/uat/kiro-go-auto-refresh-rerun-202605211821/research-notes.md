# External Research Notes

## jwadow/kiro-gateway

- `kiro/auth.py` implements token refresh with an expiration threshold and a lock around token acquisition.
- It refreshes Kiro Desktop Auth through `https://prod.{region}.auth.desktop.kiro.dev/refreshToken`.
- It refreshes AWS SSO OIDC through `https://oidc.{region}.amazonaws.com/token`.
- In SQLite/kiro-cli mode it reloads credentials before refresh and falls back to the current access token until actual expiration if a stale refresh token fails.
- `main.py` and `tests/unit/test_vpn_proxy.py` show proxy handling through `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, and `NO_PROXY` for local addresses.

## zeoak9297/KiroSwitchManager

- README claims scheduled token auto-refresh, automatic account switching, Kiro CLI import/switching, and proxy UI.
- README says only compiled installers are published and source is not available, so it is product-behavior reference rather than implementation reference.

## Kiro-Go Comparison

- Kiro-Go already has global and per-account outbound proxy support, including auth client proxy support.
- The current failure was not reproduced as an IP/proxy problem. After rebuild, UI-triggered auto-refresh succeeded for all 24 accounts.
- The additional code change in this rerun makes `autoRefreshDelay` deterministic by calculating delay from the supplied `now`, matching the scheduling intent and making the regression test exact.
