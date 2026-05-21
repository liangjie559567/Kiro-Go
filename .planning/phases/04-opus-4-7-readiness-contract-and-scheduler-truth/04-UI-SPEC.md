---
phase: 04
slug: opus-4-7-readiness-contract-and-scheduler-truth
status: approved
shadcn_initialized: false
preset: none
created: 2026-05-21
---

# Phase 04 - UI Design Contract

> Visual and interaction contract for the admin-facing Opus 4.7 readiness evidence in Phase 4.

---

## Design System

| Property | Value |
|----------|-------|
| Tool | none |
| Preset | existing admin UI |
| Component library | none |
| Icon library | none |
| Font | existing system font stack |

Phase 4 must preserve the existing single-file admin UI in `web/index.html`. Any UI change should extend the current Fleet readiness card, not introduce a new design system, build step, icon package, or large layout rewrite.

---

## Spacing Scale

Declared values follow the existing admin panel rhythm and must remain compact:

| Token | Value | Usage |
|-------|-------|-------|
| xs | 4px | Inline label/value gaps |
| sm | 8px | Status chips, small metric spacing |
| md | 16px | Card body groups |
| lg | 24px | Section spacing within the API/readiness area |

Exceptions: none. Do not create large marketing-style sections for operational readiness data.

---

## Typography

| Role | Size | Weight | Line Height |
|------|------|--------|-------------|
| Body | existing body size | 400 | existing |
| Label | existing small label size | 600 | existing |
| Heading | existing card title size | 600 | existing |
| Metric | existing body/card metric size | 600 | existing |

All readiness text must fit in the existing card width on desktop and mobile. Long reason-code arrays should wrap naturally and must not overflow or overlap adjacent content.

---

## Color

Use the existing admin palette only.

| Role | Value | Usage |
|------|-------|-------|
| Dominant | existing page background | Page and card background |
| Secondary | existing card/border colors | Metric rows and separators |
| Accent | existing info/status colors | Healthy/degraded/blocked status chips |
| Destructive | existing danger color | Blocked/error states only |

Status mapping:
- `healthy`: existing success/positive styling.
- `degraded`: existing warning/amber styling.
- `blocked`: existing danger/red styling.

Do not introduce a one-off Opus color theme.

---

## Copywriting Contract

| Element | Copy |
|---------|------|
| Card title | `Opus 4.7 fleet health` |
| Contract version label | `Contract` |
| Status label | `Status` |
| Safe concurrency label | `Safe concurrency` |
| Retry-after label | `Retry after` |
| Reason codes label | `Reasons` |
| Content evidence label | `Real content success` |
| Fallback evidence label | `Stable fallbacks` |
| Empty/unavailable state | `Fleet readiness unavailable` |
| Error state | `Fleet readiness unavailable` plus the safe error message already returned by fetch handling |

Copy must remain operational and scannable. Do not add explanatory tutorial text, feature descriptions, or inline implementation notes to the UI.

---

## Interaction Contract

- The existing refresh button remains the primary interaction.
- The card must show enough fields to compare API evidence quickly: contract version, status, safe concurrency, locally schedulable account count, retry-after, recommended action, reason codes, real content-success evidence, and stable fallback evidence.
- Account-level rows, if rendered, must use existing compact row/card patterns and show eligibility/reason-code parity with scheduler preview without exposing secrets.
- Masked account emails remain masked.
- Runtime secrets, tokens, raw headers, CLI paths with sensitive home details, and `data/config.json` contents must never be rendered.

---

## Registry Safety

| Registry | Blocks Used | Safety Gate |
|----------|-------------|-------------|
| none | none | not applicable |

---

## Checker Sign-Off

- [x] Dimension 1 Copywriting: PASS
- [x] Dimension 2 Visuals: PASS
- [x] Dimension 3 Color: PASS
- [x] Dimension 4 Typography: PASS
- [x] Dimension 5 Spacing: PASS
- [x] Dimension 6 Registry Safety: PASS

**Approval:** approved 2026-05-21
