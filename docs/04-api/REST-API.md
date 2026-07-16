# ModelMesh — REST API Specification

**Status:** Draft (pre-implementation)
**Document type:** API Specification
**API version:** v1
**Last updated:** 2026-07-16
**Owner:** Engineering
**Related:** [PRD](../PRD.md) · [High-Level Architecture](../02-architecture/High-Level-Architecture.md) · [Request Lifecycle](../02-architecture/Request-Lifecycle.md) · [Component Handbook](../03-components/README.md)

---

## 0. How to Read This Document

This is the complete external contract for ModelMesh. Applications integrate against **this API only** — they never call OpenAI, Anthropic, or any provider directly. ModelMesh presents one unified, provider-agnostic surface and internally performs routing, caching, circuit breaking, budgeting, and observability (see the [Request Lifecycle](../02-architecture/Request-Lifecycle.md)).

The document is organized as:

1. **Cross-cutting conventions** (§1–§8): design principles, versioning, naming, the common response and error formats, status-code rules, pagination, and rate limiting. These apply to **every** endpoint.
2. **Endpoint reference** (§9–§10): each endpoint documented with path, method, purpose, request/response schema, validation, errors, status codes, examples, edge cases, and rate-limit notes.
3. **OpenAPI skeleton** (§11) and **traceability** (§12).

No implementation code appears here. OpenAPI/JSON artifacts are specification, not implementation. Scope is restricted to the portfolio feature set; authentication, OAuth, RBAC, billing, tenant, admin, and SDK APIs are explicitly **out of scope**.

> **Note on auth:** because the project is single-tenant and self-hosted, the API is **unauthenticated by design**. Every endpoint below assumes trusted network access. This is a deliberate scope decision from the [PRD Non-Goals](../PRD.md), not an omission.

---

## 1. Design Principles

| # | Principle | Consequence in this API |
|---|-----------|------------------------|
| P-1 | **Provider-agnostic surface** | Request/response shapes never expose provider-specific fields. The caller cannot tell which provider served a request from the response *body*. |
| P-2 | **Familiar, low-friction shapes** | Chat/embeddings resources use widely-understood field names (`messages`, `choices`, `usage`) so migration from a provider SDK is mechanical. |
| P-3 | **Gateway decisions are observable, not intrusive** | Routing/cache/cost outcomes are surfaced via `X-ModelMesh-*` **headers** and an opt-in `modelmesh` body block — never mixed into the core resource. |
| P-4 | **Consistent envelopes** | One success-shape philosophy, one error object, one metadata convention across all endpoints. |
| P-5 | **Versioned & extensible** | URI-versioned (`/v1`), additive-by-default, with a documented deprecation policy. |
| P-6 | **Safe defaults** | Caching on, fallback on, router-chosen model when unspecified. Callers override explicitly. |
| P-7 | **Correlatable** | Every response carries a `request_id` (header + error body) for tracing against logs/metrics. |

---

## 2. Base URL & Versioning

**Base URL**
```
http://<host>:<port>
```

**Versioning strategy — URI-based, major-version only.**

- All resource and operational endpoints are prefixed with `/v1`. Example: `POST /v1/chat/completions`.
- **Infrastructure endpoints are unversioned** by convention: `GET /metrics`, `GET /healthz`, `GET /readyz`. These are consumed by scrapers/orchestrators, not application code.
- Only **breaking** changes bump the major version (`/v2`). Breaking = removing/renaming a field, changing a type, changing required-ness, or changing an error contract.
- **Additive** changes (new optional request fields, new response fields, new endpoints, new enum values) ship within `/v1` without a version bump. Clients **must ignore unknown fields**.
- **Deprecation policy:** a deprecated field/endpoint is announced in this doc, marked with a `Deprecation` response header and a `Sunset` header (RFC 8594) carrying the removal date, and kept for at least one minor cycle before a major bump removes it.

> Header-based versioning (`Accept: application/vnd.modelmesh.v1+json`) was considered and rejected for this project: URI versioning is simpler to route, cache, and demonstrate, at the cost of URL churn on major bumps — acceptable here.

---

## 3. API Naming Conventions

| Aspect | Convention | Example |
|--------|-----------|---------|
| Path segments | lowercase, **hyphenated**, plural nouns for collections | `/v1/circuit-breakers`, `/v1/providers` |
| Resource actions | prefer nouns + HTTP verbs; RPC-ish debug ops live under `/v1/debug/*` | `POST /v1/debug/route` |
| JSON field names | **`snake_case`** | `finish_reason`, `prompt_tokens` |
| Enums | lowercase `snake_case` strings | `"half_open"`, `"budget_exceeded"` |
| Resource type discriminator | `object` field on every resource | `"object": "chat.completion"` |
| IDs | opaque, prefixed strings | `req_...`, `chatcmpl_...`, `emb_...` |
| Booleans | positive phrasing, `is_`/`allow_`/`enabled` where it aids clarity | `allow_fallback`, `enabled` |
| Money | decimal string or number in USD, field suffixed `_usd`; `currency` stated where relevant | `spent_usd`, `estimated_cost_usd` |
| Timestamps | **Two documented conventions (deliberate):** resource `created` fields use **Unix epoch seconds (integer)** for ecosystem compatibility; all operational `*_at` fields use **RFC 3339 UTC strings** | `"created": 1752624000`, `"opened_at": "2026-07-16T00:00:00Z"` |
| Durations | integer, unit-suffixed field name | `uptime_s`, `cooldown_remaining_s`, `latency_ms` |

All requests and responses are `application/json; charset=utf-8`, except `GET /metrics` (Prometheus text exposition) and SSE streaming responses (`text/event-stream`).

---

## 4. Common Response Format

ModelMesh uses a **resource-direct** success shape (no `{ "data": ... }` wrapper for singular resources), consistent with P-2. Two rules make it uniform:

### 4.1 Resource discriminator
Every resource response includes an `object` field naming its type (`chat.completion`, `embedding`, `list`, `model`, `provider`, `health`, `budget`, `cache.stats`, `circuit.status`, `route.explanation`). Collections use `object: "list"` with a `data` array.

### 4.2 ModelMesh metadata (headers on every response)

Operational outcome is returned via headers so the resource body stays provider-agnostic:

| Header | Present on | Meaning |
|--------|-----------|---------|
| `X-ModelMesh-Request-Id` | all | Correlation id (also `req_...`), echo of/assigned per request |
| `X-ModelMesh-Provider` | completions, embeddings | Provider that served the response (e.g. `openai`) |
| `X-ModelMesh-Model` | completions, embeddings | Concrete model used |
| `X-ModelMesh-Cache` | completions, embeddings | `none` \| `l1` \| `l2` \| `l3` |
| `X-ModelMesh-Cost-Usd` | completions, embeddings | Committed cost of this request (0 on cache hit) |
| `X-ModelMesh-Latency-Ms` | all | Gateway-measured end-to-end latency |
| `X-ModelMesh-Fallback` | completions, embeddings | `true` if a non-primary candidate served it |
| `Retry-After` | `429`, `503` | Seconds to wait before retrying |
| `Deprecation` / `Sunset` | deprecated routes | Deprecation signaling (§2) |

### 4.3 Opt-in in-body metadata (`explain`)
For `chat/completions` and `embeddings`, setting `"explain": true` (or header `X-ModelMesh-Explain: true`) attaches a `modelmesh` object to the response body mirroring the routing/cache/cost decision (see [§10.1](#101-post-v1chatcompletions)). Off by default to keep bodies clean.

**Canonical completion success shape:**
```json
{
  "id": "chatcmpl_9s8...",
  "object": "chat.completion",
  "created": 1752624000,
  "model": "gpt-4o",
  "provider": "openai",
  "choices": [
    { "index": 0, "message": { "role": "assistant", "content": "..." }, "finish_reason": "stop" }
  ],
  "usage": { "prompt_tokens": 42, "completion_tokens": 128, "total_tokens": 170 }
}
```

---

## 5. Error Response Format

**Every** non-2xx response uses one consistent envelope. There is exactly one error shape across the whole API.

```json
{
  "error": {
    "type": "budget_exceeded",
    "code": "budget.window_limit_reached",
    "message": "Request would exceed the configured budget for window 'daily'.",
    "param": null,
    "request_id": "req_9s8d7f...",
    "provider": null,
    "retryable": false,
    "details": {}
  }
}
```

| Field | Type | Meaning |
|-------|------|---------|
| `type` | enum string | Broad, stable category (drives client handling). See taxonomy below. |
| `code` | string | Finer machine-readable sub-reason, dot-namespaced. Additive over time. |
| `message` | string | Human-readable, safe to log; never contains secrets or raw provider payloads. |
| `param` | string \| null | Offending request field for validation errors (e.g. `messages[0].role`). |
| `request_id` | string | Correlation id, matches `X-ModelMesh-Request-Id`. |
| `provider` | string \| null | Provider implicated in an upstream error, if any. |
| `retryable` | boolean | Whether the same request may succeed on retry. |
| `details` | object | Optional structured context (e.g. `retry_after_s`, validation error list). |

### 5.1 Error type taxonomy → HTTP status

These map directly onto the normalized `ProviderError` model from the [Provider Layer](../03-components/01-provider-layer.md) and the failure branches in the [Request Lifecycle §10](../02-architecture/Request-Lifecycle.md).

| `type` | HTTP | `retryable` | Raised when |
|--------|------|-------------|-------------|
| `invalid_request` | 400 | false | Malformed JSON, schema violation, invalid parameter value. |
| `not_found` | 404 | false | Unknown model/provider id or route. |
| `budget_exceeded` | 402 | false | Pre-authorization or policy rejects the request on budget. |
| `rate_limited` | 429 | true | Gateway-level protective limit hit (see §8). Includes `Retry-After`. |
| `no_healthy_provider` | 503 | true | Routing found zero eligible candidates (all unhealthy/open-circuit). |
| `provider_error` | 502 | true | An upstream provider returned an error and all candidates were exhausted. |
| `upstream_timeout` | 504 | true | Provider call(s) timed out and candidates were exhausted. |
| `internal_error` | 500 | false | Unexpected gateway fault. |
| `service_unavailable` | 503 | true | Gateway not ready (startup, dependency down) — from readiness gating. |

**402 for `budget_exceeded`** is a deliberate, documented choice: *Payment Required* is the closest-fitting semantic for a spend/quota limit and reads clearly in client code and logs.

---

## 6. HTTP Status Code Guidelines

| Code | Name | Used for |
|------|------|----------|
| `200 OK` | Success | All successful reads and completions/embeddings (including cache hits). |
| `202 Accepted` | Accepted | *Not used* in v1 — all supported operations are synchronous. Reserved. |
| `400 Bad Request` | Validation | Malformed body or invalid parameters (`invalid_request`). |
| `402 Payment Required` | Budget | `budget_exceeded`. |
| `404 Not Found` | Missing | Unknown route or resource id (`not_found`). |
| `405 Method Not Allowed` | Wrong verb | Correct path, unsupported method. Includes `Allow` header. |
| `409 Conflict` | Conflict | *Not used* in v1 (no mutable resources). Reserved. |
| `415 Unsupported Media Type` | Content type | Non-JSON body on a JSON endpoint. |
| `422 Unprocessable Entity` | — | **Not used**; validation is unified under `400` to keep one path. Documented to avoid ambiguity. |
| `429 Too Many Requests` | Rate limit | `rate_limited`. Includes `Retry-After`, `X-RateLimit-*`. |
| `500 Internal Server Error` | Fault | `internal_error`. |
| `502 Bad Gateway` | Upstream | `provider_error` after candidate exhaustion. |
| `503 Service Unavailable` | Unavailable | `no_healthy_provider`, `service_unavailable`. Includes `Retry-After`. |
| `504 Gateway Timeout` | Upstream timeout | `upstream_timeout` after candidate exhaustion. |

**Principle:** a *single provider* failing is **not** a caller-facing error — the orchestrator falls back across candidates. Only **exhaustion of all candidates** surfaces as `502`/`503`/`504`. This mirrors the "only three caller-facing failures" invariant (validation, budget, exhaustion).

---

## 7. Pagination Strategy

The v1 collection endpoints (`/v1/providers`, `/v1/models`) return **complete, bounded lists** — their size is governed by configuration (a handful to low-hundreds of entries), so pagination is **not applied in v1**. Responses use `object: "list"` with a `data` array and a `has_more: false` field.

A **cursor-based** pagination contract is reserved for forward compatibility; if a collection grows unbounded it will adopt, without a major bump (additive):

| Query param | Type | Meaning |
|-------------|------|---------|
| `limit` | int (1–100, default 100) | Page size. |
| `cursor` | string | Opaque forward cursor from a prior response. |

Response gains: `has_more` (bool) and `next_cursor` (string \| null). Cursor (not offset) is chosen for stable iteration under concurrent change. Clients should already treat `has_more`/`next_cursor` as authoritative.

---

## 8. Rate Limiting Notes

Per the scope, there is **no per-tenant/auth-based** rate limiting. An **optional, global protective limiter** may be enabled (config-driven) to shield the gateway and upstream providers from overload. When enabled:

- Exceeding the limit returns `429` with `type: "rate_limited"`.
- Headers: `Retry-After: <seconds>`, `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` (epoch seconds).
- The limit is a coarse global/per-IP token bucket, **not** billing or quota.

This is distinct from **budget** (§5, `402`), which is about cost, and from **circuit breaking** (upstream health). Individual endpoints note applicability under *Rate Limiting*.

---

## 9. Endpoint Catalog

| Endpoint | Method | Purpose | Section |
|----------|--------|---------|---------|
| `/v1/chat/completions` | POST | Unified chat completion | [10.1](#101-post-v1chatcompletions) |
| `/v1/embeddings` | POST | Unified text embeddings | [10.2](#102-post-v1embeddings) |
| `/v1/providers` | GET | List configured providers + status | [10.3](#103-get-v1providers) |
| `/v1/models` | GET | List available models | [10.4](#104-get-v1models) |
| `/v1/health` | GET | Aggregate health status | [10.5](#105-get-v1health-healthz-readyz) |
| `/healthz`, `/readyz` | GET | Liveness / readiness probes | [10.5](#105-get-v1health-healthz-readyz) |
| `/metrics` | GET | Prometheus metrics exposition | [10.6](#106-get-metrics) |
| `/v1/budget` | GET | Budget status *(stretch)* | [10.7](#107-get-v1budget-stretch) |
| `/v1/debug/route` | POST | Router explanation *(debug)* | [10.8](#108-post-v1debugroute-router-explanation-debug) |
| `/v1/cache/stats` | GET | Cache statistics | [10.9](#109-get-v1cachestats) |
| `/v1/circuit-breakers` | GET | Circuit breaker status | [10.10](#1010-get-v1circuit-breakers) |

---

## 10. Endpoint Reference

---

### 10.1 `POST /v1/chat/completions`

**Purpose.** The primary endpoint: accept a provider-agnostic chat request and return a completion, with ModelMesh performing classification, routing, caching, budgeting, circuit breaking, and (optionally) shadowing internally.

**Request schema**

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `model` | string | no | `"auto"` | ModelMesh model alias, or `"auto"` to let the router choose. If a concrete alias is given it acts as a **routing constraint** (see `routing`). |
| `messages` | array | **yes** | — | Ordered conversation turns. Non-empty. |
| `messages[].role` | enum | yes | — | `system` \| `user` \| `assistant`. |
| `messages[].content` | string | yes | — | Message text. Non-empty (except assistant). |
| `max_tokens` | integer | no | provider default | Upper bound on generated tokens. `> 0`. |
| `temperature` | number | no | `1.0` | Sampling temperature, `0.0`–`2.0`. |
| `top_p` | number | no | `1.0` | Nucleus sampling, `0.0`–`1.0`. |
| `stop` | string \| string[] | no | none | Up to 4 stop sequences. |
| `stream` | boolean | no | `false` | If true, response is SSE (`text/event-stream`). |
| `routing` | object | no | — | Routing hints/constraints (below). |
| `routing.providers` | string[] | no | — | Restrict candidates to these providers. |
| `routing.models` | string[] | no | — | Restrict candidates to these models. |
| `routing.strategy` | string | no | config | Named routing strategy override. |
| `routing.allow_fallback` | boolean | no | `true` | Permit fallback across candidates. |
| `cache` | object | no | — | Per-request cache control. |
| `cache.enabled` | boolean | no | `true` | Master switch for this request. |
| `cache.bypass` | boolean | no | `false` | Skip lookup but still populate on the way back. |
| `cache.semantic` | boolean | no | `true` | Allow L3 semantic matches. |
| `explain` | boolean | no | `false` | Attach the `modelmesh` decision block to the response. |
| `metadata` | object | no | — | Opaque string-keyed labels echoed to logs/metrics (bounded size). |

**Response schema** — `object: "chat.completion"` (see §4.2 canonical shape). With `explain: true`, adds:

```json
"modelmesh": {
  "request_id": "req_...",
  "cache": { "level": "none", "semantic_similarity": null },
  "routing": {
    "selected": { "provider": "openai", "model": "gpt-4o" },
    "candidates": [
      { "provider": "openai", "model": "gpt-4o", "score": 0.71, "healthy": true },
      { "provider": "anthropic", "model": "claude-sonnet-5", "score": 0.29, "healthy": true }
    ],
    "fallback_used": false,
    "complexity": { "level": "moderate", "score": 0.52, "source": "heuristic" }
  },
  "cost": { "estimated_usd": 0.0021, "actual_usd": 0.0019 },
  "latency_ms": 812
}
```

**Streaming.** When `stream: true`, the response is `text/event-stream`: a sequence of `data: {chunk}` SSE events whose objects use `object: "chat.completion.chunk"` with incremental `choices[].delta`, terminated by `data: [DONE]`. Cache hits stream a single terminal chunk. Errors mid-stream are delivered as a final `event: error` frame carrying the standard error object.

**Validation rules**
- `messages` present, non-empty; each has valid `role` and (for user/system) non-empty `content`.
- `temperature ∈ [0,2]`, `top_p ∈ [0,1]`, `max_tokens > 0`, `stop` ≤ 4 entries.
- If `model` is a concrete alias, it must exist and be enabled → else `not_found`.
- `routing.providers`/`routing.models`, if given, must reference known ids → else `not_found`.
- Body ≤ configured max size; `Content-Type: application/json`.

**Error responses**

| Status | `type` | When |
|--------|--------|------|
| 400 | `invalid_request` | Schema/parameter violation (`param` set). |
| 402 | `budget_exceeded` | Budget pre-authorization rejects (and no reroute). |
| 404 | `not_found` | Unknown `model`/routing constraint. |
| 429 | `rate_limited` | Global limiter (if enabled). |
| 502 | `provider_error` | All candidates returned upstream errors. |
| 503 | `no_healthy_provider` | No eligible candidate (all circuits open/unhealthy). |
| 504 | `upstream_timeout` | All candidates timed out. |
| 500 | `internal_error` | Unexpected fault. |

**Status codes:** `200` (incl. cache hit), `400`, `402`, `404`, `415`, `429`, `500`, `502`, `503`, `504`.

**Example request**
```http
POST /v1/chat/completions HTTP/1.1
Content-Type: application/json

{
  "model": "auto",
  "messages": [
    { "role": "system", "content": "You are concise." },
    { "role": "user", "content": "Explain circuit breakers in one sentence." }
  ],
  "max_tokens": 60,
  "temperature": 0.3,
  "explain": true
}
```

**Example response**
```http
HTTP/1.1 200 OK
Content-Type: application/json
X-ModelMesh-Request-Id: req_9s8d7f
X-ModelMesh-Provider: openai
X-ModelMesh-Model: gpt-4o
X-ModelMesh-Cache: none
X-ModelMesh-Cost-Usd: 0.0019
X-ModelMesh-Latency-Ms: 812
X-ModelMesh-Fallback: false

{
  "id": "chatcmpl_9s8d7f",
  "object": "chat.completion",
  "created": 1752624000,
  "model": "gpt-4o",
  "provider": "openai",
  "choices": [
    { "index": 0,
      "message": { "role": "assistant", "content": "A circuit breaker stops calling a failing dependency for a cooldown so it can recover instead of cascading the failure." },
      "finish_reason": "stop" }
  ],
  "usage": { "prompt_tokens": 28, "completion_tokens": 31, "total_tokens": 59 },
  "modelmesh": { "cache": { "level": "none" }, "routing": { "fallback_used": false }, "cost": { "actual_usd": 0.0019 } }
}
```

**Edge cases**
- **Exact-cache hit:** identical prior request → `200`, `X-ModelMesh-Cache: l1|l2`, `X-ModelMesh-Cost-Usd: 0`, no provider call, no spend committed.
- **Semantic hit:** paraphrase within threshold → `X-ModelMesh-Cache: l3`, `modelmesh.cache.semantic_similarity` populated.
- **Fallback:** primary provider fails → served by next candidate, `X-ModelMesh-Fallback: true`, `modelmesh.routing.fallback_used: true`.
- **`cache.bypass: true`:** forces a fresh provider call but still populates caches.
- **Non-deterministic params** (`temperature` high): still cacheable by exact key; callers wanting freshness set `cache.enabled: false`.
- **Empty/whitespace content**, unknown role → `400` with precise `param`.

**Rate limiting.** Subject to the global limiter (§8) if enabled. No per-caller quota.

---

### 10.2 `POST /v1/embeddings`

**Purpose.** Provider-agnostic text embeddings, routed and cached like completions.

**Request schema**

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `model` | string | no | `"auto"` | Embedding model alias or `"auto"`. |
| `input` | string \| string[] | **yes** | — | One text, or a batch of texts. |
| `encoding_format` | enum | no | `"float"` | `float` (only value in v1; reserved for `base64`). |
| `routing` | object | no | — | Same shape as §10.1 `routing`. |
| `cache` | object | no | — | Same shape as §10.1 `cache` (semantic caching typically off for embeddings). |
| `explain` | boolean | no | `false` | Attach `modelmesh` block. |
| `metadata` | object | no | — | Opaque labels. |

**Response schema** — `object: "list"`:
```json
{
  "object": "list",
  "model": "text-embedding-3-small",
  "provider": "openai",
  "data": [
    { "object": "embedding", "index": 0, "embedding": [0.0123, -0.0456, "...(N dims)"] }
  ],
  "usage": { "prompt_tokens": 8, "total_tokens": 8 }
}
```

**Validation rules**
- `input` non-empty; batch size ≤ configured max; each element non-empty and ≤ max token length.
- `encoding_format ∈ {float}`.
- `model`, if concrete, must exist, be enabled, and support embeddings → else `not_found` / `invalid_request`.

**Error responses:** same taxonomy as §10.1 (`invalid_request`, `budget_exceeded`, `not_found`, `rate_limited`, `provider_error`, `no_healthy_provider`, `upstream_timeout`, `internal_error`).

**Status codes:** `200`, `400`, `402`, `404`, `415`, `429`, `500`, `502`, `503`, `504`.

**Example request**
```http
POST /v1/embeddings HTTP/1.1
Content-Type: application/json

{ "model": "auto", "input": ["circuit breaker", "semantic cache"] }
```

**Example response**
```http
HTTP/1.1 200 OK
X-ModelMesh-Provider: openai
X-ModelMesh-Model: text-embedding-3-small
X-ModelMesh-Cache: none
X-ModelMesh-Cost-Usd: 0.0000012

{
  "object": "list",
  "model": "text-embedding-3-small",
  "provider": "openai",
  "data": [
    { "object": "embedding", "index": 0, "embedding": [0.01, -0.04, "..."] },
    { "object": "embedding", "index": 1, "embedding": [0.02,  0.03, "..."] }
  ],
  "usage": { "prompt_tokens": 6, "total_tokens": 6 }
}
```

**Edge cases**
- **Batch ordering:** `data[i].index` always corresponds to `input[i]`; order is preserved even on cache hits.
- **Partial cache:** a batch may mix cached and freshly computed vectors; the response is uniform and `X-ModelMesh-Cache` reflects the dominant/`mixed` state (`mixed` is a valid header value here).
- **Dimension mismatch across models:** a batch is always served by a single model, so all vectors share dimensionality.

**Rate limiting.** Global limiter only.

---

### 10.3 `GET /v1/providers`

**Purpose.** Enumerate configured providers and their current health, for discovery and dashboards.

**Request schema.** None. No query params in v1 (pagination reserved, §7).

**Response schema** — `object: "list"` of `provider`:

| Field | Type | Description |
|-------|------|-------------|
| `data[].object` | string | `"provider"`. |
| `data[].id` | string | Provider id (e.g. `openai`). |
| `data[].name` | string | Display name. |
| `data[].enabled` | boolean | Whether configured-on. |
| `data[].status` | enum | `healthy` \| `degraded` \| `unhealthy` (from [Circuit Breaker HealthView](../03-components/04-circuit-breaker.md)). |
| `data[].circuit_state` | enum | `closed` \| `open` \| `half_open`. |
| `data[].models` | string[] | Model ids offered. |
| `has_more` | boolean | Always `false` in v1. |

**Validation / errors:** `500 internal_error` only. Health/circuit read failure degrades gracefully to `status: "unknown"` rather than failing the call.

**Status codes:** `200`, `500`.

**Example response**
```json
{
  "object": "list",
  "data": [
    { "object": "provider", "id": "openai", "name": "OpenAI", "enabled": true,
      "status": "healthy", "circuit_state": "closed",
      "models": ["gpt-4o", "text-embedding-3-small"] },
    { "object": "provider", "id": "anthropic", "name": "Anthropic", "enabled": true,
      "status": "degraded", "circuit_state": "half_open",
      "models": ["claude-sonnet-5"] }
  ],
  "has_more": false
}
```

**Edge cases.** A configured-but-disabled provider appears with `enabled: false`. A provider whose circuit is open shows `status: "unhealthy"`, `circuit_state: "open"`.

**Rate limiting.** Not applicable (cheap read).

---

### 10.4 `GET /v1/models`

**Purpose.** Enumerate available models across providers with capabilities and pricing, for discovery and cost estimation.

**Request schema.** Optional filter query params:

| Param | Type | Description |
|-------|------|-------------|
| `provider` | string | Filter to one provider id. |
| `capability` | enum | `chat` \| `embeddings`. |

**Response schema** — `object: "list"` of `model`:

| Field | Type | Description |
|-------|------|-------------|
| `data[].object` | string | `"model"`. |
| `data[].id` | string | Model alias (`gpt-4o`). |
| `data[].provider` | string | Owning provider id. |
| `data[].family` | string | Model family/label. |
| `data[].capabilities` | string[] | Subset of `["chat","embeddings"]`. |
| `data[].context_window` | integer | Max context tokens. |
| `data[].pricing` | object | `{ "input_per_1k_usd": num, "output_per_1k_usd": num, "currency": "USD" }` (from the [Cost Model](../03-components/07-budget-engine.md)). |
| `data[].enabled` | boolean | Whether routable. |

**Validation / errors:** unknown `provider` filter → `404 not_found`; invalid `capability` → `400 invalid_request`.

**Status codes:** `200`, `400`, `404`, `500`.

**Example request**
```http
GET /v1/models?capability=chat HTTP/1.1
```

**Example response**
```json
{
  "object": "list",
  "data": [
    { "object": "model", "id": "gpt-4o", "provider": "openai", "family": "gpt-4o",
      "capabilities": ["chat"], "context_window": 128000,
      "pricing": { "input_per_1k_usd": 0.0025, "output_per_1k_usd": 0.01, "currency": "USD" },
      "enabled": true },
    { "object": "model", "id": "claude-sonnet-5", "provider": "anthropic", "family": "claude-sonnet",
      "capabilities": ["chat"], "context_window": 200000,
      "pricing": { "input_per_1k_usd": 0.003, "output_per_1k_usd": 0.015, "currency": "USD" },
      "enabled": true }
  ],
  "has_more": false
}
```

**Edge cases.** Pricing is configuration-sourced; a model missing pricing is still listed but flagged with `pricing: null` and is excluded from budget-estimated routing.

**Rate limiting.** Not applicable.

---

### 10.5 `GET /v1/health`, `/healthz`, `/readyz`

**Purpose.** Operational health. Three surfaces for three audiences:

- `GET /healthz` — **liveness** probe. Cheap, dependency-free. `200 {"status":"ok"}` if the process is up.
- `GET /readyz` — **readiness** probe. `200` only when config is loaded and critical dependencies (Redis) are reachable; `503 service_unavailable` otherwise. Used to gate traffic at startup/shutdown.
- `GET /v1/health` — **aggregate** human/dashboard view with dependency and provider detail.

**Request schema.** None.

**Response schema** (`/v1/health`) — `object: "health"`:

| Field | Type | Description |
|-------|------|-------------|
| `status` | enum | `ok` \| `degraded` \| `unhealthy`. |
| `version` | string | Build/version. |
| `uptime_s` | integer | Seconds since start. |
| `dependencies.redis.status` | enum | `ok` \| `unavailable`. |
| `dependencies.providers` | object | `{ "healthy": int, "total": int }`. |
| `checks` | array | `[{ "name": string, "status": enum, "detail": string }]`. |

**Status semantics.** `degraded` = serving but a non-critical dependency or some providers are down (e.g. Redis down → caches/budget degrade per their fail-safe rules, but requests still flow). `unhealthy` = cannot serve.

**Status codes:** `/healthz` `200`; `/readyz` `200`/`503`; `/v1/health` `200` (with `status` field even when `degraded`), `500`.

**Example response** (`/v1/health`)
```json
{
  "object": "health",
  "status": "degraded",
  "version": "0.1.0",
  "uptime_s": 36012,
  "dependencies": {
    "redis": { "status": "unavailable" },
    "providers": { "healthy": 1, "total": 2 }
  },
  "checks": [
    { "name": "redis", "status": "unavailable", "detail": "connection refused; L2/L3 degraded to miss, budget fail-closed" },
    { "name": "provider:openai", "status": "ok", "detail": "circuit closed" },
    { "name": "provider:anthropic", "status": "unhealthy", "detail": "circuit open" }
  ]
}
```

**Edge cases.** `/v1/health` returns `200` even when `status: "degraded"` — degraded is a body signal, not an HTTP failure. `/readyz` is the only health surface that returns `503`, because orchestrators act on its status.

**Rate limiting.** Never rate-limited (probes must always answer).

---

### 10.6 `GET /metrics`

**Purpose.** Prometheus metrics exposition for scraping (the metrics catalog defined in [Observability](../03-components/05-observability.md) and [Request Lifecycle §4](../02-architecture/Request-Lifecycle.md)).

**Request schema.** None. Unversioned by convention (§2).

**Response.** `200 OK`, `Content-Type: text/plain; version=0.0.4; charset=utf-8` — Prometheus text exposition format, **not JSON**. This is the one endpoint that does not follow §4/§5.

**Example response (excerpt)**
```text
# HELP requests_completed_total Completed gateway requests
# TYPE requests_completed_total counter
requests_completed_total{outcome="ok",cache_level="l1"} 5821
requests_completed_total{outcome="ok",cache_level="none"} 10233
# HELP provider_latency_seconds Provider call latency
# TYPE provider_latency_seconds histogram
provider_latency_seconds_bucket{provider="openai",model="gpt-4o",le="0.5"} 812
# TYPE circuit_state gauge
circuit_state{provider="anthropic"} 1
```

**Status codes:** `200`, `500`.

**Edge cases / errors.** Errors return plain text, not the JSON error envelope, so scrapers aren't confused. Never rate-limited.

**Rate limiting.** Not applicable (scrape endpoint).

---

### 10.7 `GET /v1/budget` *(stretch)*

**Purpose.** Report current spend against configured budgets ([Budget Engine](../03-components/07-budget-engine.md)). Marked **stretch** per scope.

**Request schema.** Optional `scope` query param to filter to one budget scope (`global`, `provider:<id>`, `model:<id>`).

**Response schema** — `object: "budget"`:

| Field | Type | Description |
|-------|------|-------------|
| `scopes[].scope` | string | Budget scope id. |
| `scopes[].window` | enum | `daily` \| `monthly` \| `rolling`. |
| `scopes[].limit_usd` | number | Configured limit. |
| `scopes[].spent_usd` | number | Committed spend in window. |
| `scopes[].remaining_usd` | number | `limit - spent` (≥ 0). |
| `scopes[].utilization` | number | `spent/limit`, 0–1. |
| `scopes[].resets_at` | string (RFC3339) | Next window reset. |
| `scopes[].enforcement` | enum | `hard` \| `soft`. |

**Validation / errors:** unknown `scope` → `404 not_found`. If the spend store (Redis) is unavailable, returns `200` with `spent_usd: null` and a `stale: true` flag rather than failing (read-only view).

**Status codes:** `200`, `404`, `500`.

**Example response**
```json
{
  "object": "budget",
  "scopes": [
    { "scope": "global", "window": "daily", "limit_usd": 25.0,
      "spent_usd": 18.42, "remaining_usd": 6.58, "utilization": 0.7368,
      "resets_at": "2026-07-17T00:00:00Z", "enforcement": "hard" }
  ]
}
```

**Edge cases.** `utilization ≥ 1.0` with `enforcement: "hard"` means new billable requests will be rejected with `402` until `resets_at`. `soft` scopes never reject; they only report/alert.

**Rate limiting.** Not applicable.

---

### 10.8 `POST /v1/debug/route` (Router Explanation) *(debug)*

**Purpose.** Explain what ModelMesh **would do** for a given request — routing candidates, complexity, cache decision, and cost estimate — **without calling any provider and without committing spend or cache writes**. A dry-run introspection tool for development and demos ([Routing Engine](../03-components/02-routing-engine.md)).

**Request schema.** Accepts the **same body as `POST /v1/chat/completions`** (or `/v1/embeddings` via `"task": "embeddings"`). No completion is generated.

| Field | Type | Description |
|-------|------|-------------|
| `task` | enum | `chat` (default) \| `embeddings`. |
| *(+ all chat/embeddings request fields)* | | Interpreted for routing only; `stream` ignored. |

**Response schema** — `object: "route.explanation"`:

| Field | Type | Description |
|-------|------|-------------|
| `selected` | object | `{ provider, model }` the router would pick first. |
| `candidates` | array | `[{ provider, model, score, healthy, circuit_state, reason }]`, ordered. |
| `complexity` | object | `{ level, score, source, fallback }` from the classifier. |
| `cache` | object | `{ would_check: bool, exact_key_preview: string, semantic_enabled: bool }` — a hashed key preview, never raw content. |
| `budget` | object | `{ estimated_cost_usd, decision, remaining_usd }` (`decision`: `allow`/`reject`/`reroute`). |
| `explanation` | string | Human-readable summary of the decision. |

**Validation / errors:** same body validation as the target task (`400 invalid_request`, `404 not_found`). This endpoint itself does not produce `402`/`5xx` upstream errors (it never calls providers); it *reports* the budget decision as data.

**Status codes:** `200`, `400`, `404`, `500`.

**Example request**
```http
POST /v1/debug/route HTTP/1.1
Content-Type: application/json

{ "task": "chat", "model": "auto",
  "messages": [ { "role": "user", "content": "Prove that sqrt(2) is irrational." } ] }
```

**Example response**
```json
{
  "object": "route.explanation",
  "selected": { "provider": "anthropic", "model": "claude-sonnet-5" },
  "candidates": [
    { "provider": "anthropic", "model": "claude-sonnet-5", "score": 0.62, "healthy": true, "circuit_state": "closed", "reason": "complexity=complex → strong model, weight 0.6" },
    { "provider": "openai", "model": "gpt-4o", "score": 0.38, "healthy": true, "circuit_state": "closed", "reason": "fallback candidate" }
  ],
  "complexity": { "level": "complex", "score": 0.81, "source": "heuristic", "fallback": false },
  "cache": { "would_check": true, "exact_key_preview": "sha256:4f9a...", "semantic_enabled": true },
  "budget": { "estimated_cost_usd": 0.0043, "decision": "allow", "remaining_usd": 6.58 },
  "explanation": "Prompt classified complex; weighted strategy ranks claude-sonnet-5 first among healthy providers; within budget; cache would be consulted before dispatch."
}
```

**Edge cases.** If all providers are unhealthy, `selected: null`, `candidates: []`, and `explanation` states no eligible candidate (this is *reported*, not a `503`, since nothing is dispatched). Reflects the same decision logic the live path uses, so explanations stay truthful.

**Rate limiting.** Debug endpoint; global limiter applies if enabled. Intended for non-production/manual use.

---

### 10.9 `GET /v1/cache/stats`

**Purpose.** Report multi-level cache effectiveness ([Cache System](../03-components/03-cache-system.md)) for observability and demos.

**Request schema.** Optional `window` query param (`session` since start | `1h` | `24h`), default `session`.

**Response schema** — `object: "cache.stats"`:

| Field | Type | Description |
|-------|------|-------------|
| `window` | string | Reporting window. |
| `levels.l1` | object | `{ hits, misses, hit_ratio, entries, evictions }`. |
| `levels.l2` | object | `{ hits, misses, hit_ratio, entries, backend_errors }`. |
| `levels.l3` | object | `{ hits, misses, hit_ratio, entries, avg_similarity, backend_errors }`. |
| `overall.hit_ratio` | number | Combined hit ratio across levels. |
| `overall.provider_calls_avoided` | integer | Requests served without a provider call. |
| `overall.estimated_cost_saved_usd` | number | Estimated spend avoided by caching. |

**Validation / errors:** invalid `window` → `400 invalid_request`. L1 stats are per-instance (this instance); L2/L3 are fleet-wide (Redis-backed) — noted in the response via `l1.scope: "instance"` vs `l2.scope: "fleet"`. If Redis is unavailable, L2/L3 report `null` with `stale: true`; L1 still reports.

**Status codes:** `200`, `400`, `500`.

**Example response**
```json
{
  "object": "cache.stats",
  "window": "session",
  "levels": {
    "l1": { "scope": "instance", "hits": 4120, "misses": 8800, "hit_ratio": 0.319, "entries": 5000, "evictions": 231 },
    "l2": { "scope": "fleet", "hits": 1701, "misses": 7099, "hit_ratio": 0.193, "entries": 21894, "backend_errors": 0 },
    "l3": { "scope": "fleet", "hits": 388, "misses": 6711, "hit_ratio": 0.055, "entries": 9042, "avg_similarity": 0.94, "backend_errors": 0 }
  },
  "overall": { "hit_ratio": 0.42, "provider_calls_avoided": 6209, "estimated_cost_saved_usd": 11.87 }
}
```

**Edge cases.** Ratios are `0` when denominators are `0` (no `NaN`). `avg_similarity` reported only over L3 hits.

**Rate limiting.** Not applicable.

---

### 10.10 `GET /v1/circuit-breakers`

**Purpose.** Report per-provider circuit and health state ([Circuit Breaker](../03-components/04-circuit-breaker.md)) for observability and demonstrating resilience under fault injection.

**Request schema.** Optional `provider` query param to filter to one provider.

**Response schema** — `object: "list"` of `circuit.status`:

| Field | Type | Description |
|-------|------|-------------|
| `data[].object` | string | `"circuit.status"`. |
| `data[].provider` | string | Provider id. |
| `data[].state` | enum | `closed` \| `open` \| `half_open`. |
| `data[].health_score` | number | 0–1 composite health. |
| `data[].failure_rate` | number | Recent window failure rate, 0–1. |
| `data[].window` | object | `{ requests, failures, rolling_seconds }`. |
| `data[].opened_at` | string \| null | RFC3339 when circuit last opened. |
| `data[].cooldown_remaining_s` | integer \| null | Seconds until half-open probe (if open). |
| `data[].last_transition` | object | `{ from, to, at }`. |

**Validation / errors:** unknown `provider` → `404 not_found`. If the shared health store is unavailable, returns `200` with `state` from the instance-local view and `stale: true` (consistent with the breaker's fail-open-on-store-outage default).

**Status codes:** `200`, `404`, `500`.

**Example response**
```json
{
  "object": "list",
  "data": [
    { "object": "circuit.status", "provider": "openai", "state": "closed",
      "health_score": 0.99, "failure_rate": 0.004,
      "window": { "requests": 2451, "failures": 10, "rolling_seconds": 60 },
      "opened_at": null, "cooldown_remaining_s": null,
      "last_transition": { "from": "half_open", "to": "closed", "at": "2026-07-15T22:10:03Z" } },
    { "object": "circuit.status", "provider": "anthropic", "state": "open",
      "health_score": 0.12, "failure_rate": 0.83,
      "window": { "requests": 120, "failures": 100, "rolling_seconds": 60 },
      "opened_at": "2026-07-16T00:01:00Z", "cooldown_remaining_s": 18,
      "last_transition": { "from": "closed", "to": "open", "at": "2026-07-16T00:01:00Z" } }
  ],
  "has_more": false
}
```

**Edge cases.** A `half_open` provider shows `cooldown_remaining_s: 0` and is accepting limited probe traffic. `stale: true` signals the numbers are from a single instance's local view during a store outage.

**Rate limiting.** Not applicable.

---

## 11. OpenAPI Skeleton (excerpt)

An OpenAPI 3.1 fragment illustrating the shared components and one path; the full spec would enumerate all §10 paths against these schemas.

```yaml
openapi: 3.1.0
info:
  title: ModelMesh API
  version: "1.0.0"
  description: Unified, provider-agnostic LLM gateway API.
servers:
  - url: /v1
components:
  schemas:
    Error:
      type: object
      required: [error]
      properties:
        error:
          type: object
          required: [type, code, message, request_id, retryable]
          properties:
            type:
              type: string
              enum: [invalid_request, not_found, budget_exceeded, rate_limited,
                     no_healthy_provider, provider_error, upstream_timeout,
                     internal_error, service_unavailable]
            code: { type: string }
            message: { type: string }
            param: { type: [string, "null"] }
            request_id: { type: string }
            provider: { type: [string, "null"] }
            retryable: { type: boolean }
            details: { type: object }
    ChatMessage:
      type: object
      required: [role, content]
      properties:
        role: { type: string, enum: [system, user, assistant] }
        content: { type: string }
    ChatCompletionRequest:
      type: object
      required: [messages]
      properties:
        model: { type: string, default: auto }
        messages:
          type: array
          minItems: 1
          items: { $ref: '#/components/schemas/ChatMessage' }
        max_tokens: { type: integer, minimum: 1 }
        temperature: { type: number, minimum: 0, maximum: 2, default: 1 }
        top_p: { type: number, minimum: 0, maximum: 1, default: 1 }
        stop:
          oneOf:
            - { type: string }
            - { type: array, items: { type: string }, maxItems: 4 }
        stream: { type: boolean, default: false }
        explain: { type: boolean, default: false }
    Usage:
      type: object
      properties:
        prompt_tokens: { type: integer }
        completion_tokens: { type: integer }
        total_tokens: { type: integer }
    ChatCompletion:
      type: object
      required: [id, object, created, model, provider, choices, usage]
      properties:
        id: { type: string }
        object: { type: string, const: chat.completion }
        created: { type: integer, description: Unix epoch seconds }
        model: { type: string }
        provider: { type: string }
        choices:
          type: array
          items:
            type: object
            properties:
              index: { type: integer }
              message: { $ref: '#/components/schemas/ChatMessage' }
              finish_reason: { type: string, enum: [stop, length, content_filter, error] }
        usage: { $ref: '#/components/schemas/Usage' }
  responses:
    BudgetExceeded:
      description: Request would exceed configured budget.
      content:
        application/json:
          schema: { $ref: '#/components/schemas/Error' }
paths:
  /chat/completions:
    post:
      summary: Create a chat completion
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/ChatCompletionRequest' }
      responses:
        "200":
          description: Completion (fresh or cached)
          headers:
            X-ModelMesh-Cache: { schema: { type: string, enum: [none, l1, l2, l3] } }
            X-ModelMesh-Provider: { schema: { type: string } }
            X-ModelMesh-Cost-Usd: { schema: { type: number } }
          content:
            application/json:
              schema: { $ref: '#/components/schemas/ChatCompletion' }
        "400": { $ref: '#/components/responses/BudgetExceeded' }   # illustrative reuse
        "402": { $ref: '#/components/responses/BudgetExceeded' }
```

---

## 12. Traceability

| API surface | Backing module | Phase |
|-------------|----------------|-------|
| `POST /v1/chat/completions`, `/v1/embeddings` | [Provider Layer](../03-components/01-provider-layer.md) (execution) + whole pipeline | 1 |
| `model: "auto"`, `routing.*`, `/v1/debug/route` | [Routing Engine](../03-components/02-routing-engine.md) | 2 |
| `X-ModelMesh-Cache`, `/v1/cache/stats`, `cache.*` | [Cache System](../03-components/03-cache-system.md) | 3 |
| `/v1/circuit-breakers`, `no_healthy_provider`, fallback | [Circuit Breaker](../03-components/04-circuit-breaker.md) | 4 |
| `/metrics`, `X-ModelMesh-*`, `/v1/health` | [Observability](../03-components/05-observability.md) | 5 |
| `X-ModelMesh-Fallback`, target distribution | [Load Balancer](../03-components/06-load-balancer.md) | 6 |
| `402 budget_exceeded`, `/v1/budget`, `X-ModelMesh-Cost-Usd` | [Budget Engine](../03-components/07-budget-engine.md) | 7 |
| `complexity` in explain / debug | [Prompt Complexity Classifier](../03-components/08-prompt-complexity-classifier.md) | 8 |
| *(internal; no direct public endpoint — surfaced only via metrics)* | [Shadow Traffic](../03-components/09-shadow-traffic.md) | 9 |

> **Shadow traffic is intentionally not a public endpoint.** It is a server-side behavior configured via deployment config and observed via `/metrics`; exposing it as an API is out of scope and would contradict its out-of-band nature.
