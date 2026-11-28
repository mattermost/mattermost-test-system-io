# SEC-11524: Error Signature Normalizer & Top-50 Validation Report

**Ticket:** `SEC-11524` (Phase 1)  
**Package:** `apps/server/internal/errorsig` (`AlgoVersion = 1`)  
**Corpus:** `apps/server/internal/errorsig/testdata/golden_corpus.json`  
**Status:** `algo_version = 1` frozen for `SEC-11519`

---

## 1. Frozen `algo_version = 1` Decision

**Decision:** **`top_frame` is EXCLUDED from the `fingerprint` SHA-256 hash in `algo_version = 1`**, and **`top_frame` is ALWAYS extracted and stored unhashed** on `failure_signatures.top_frame`.

```text
fingerprint = sha256(algo_version + "|" + error_class + "|" + normalized_message)
```

### Rationale for `SEC-11519`
1. **Preserves cross-test root-cause grouping ("one root cause across 40 tests -> one signature row"):** Hashing `top_frame` splits shared infrastructure, backend, and helper failures (such as `ECONNREFUSED`, `502 Bad Gateway`, `StatusRuntimeException: UNAVAILABLE`, `DetoxRuntimeError`, `Target page, context or browser has been closed`, and `spec-did-not-run`) into per-spec and per-line signatures (`22` false splits across `54` validation failures).
2. **Protects AI triage verdict reuse (`SEC-11517` / `SEC-11514`) and KPI #4 accuracy:** When a spec line shifts in a later commit or a shared failure hits multiple specs, excluding `top_frame` from the hash keeps the `fingerprint` stable so cached verdicts hit and recurring-failure counts aggregate accurately.
3. **Keeps call-site context without hash fragmentation:** `ExtractTopFrame` always scans `ErrorStack` first and falls back to `ErrorMessage` lines after line 1 (critical for Detox and Maestro where `ingest` stores `ErrorStack = nil`), skipping runtime frames (`node_modules/`, `node:internal/`, `playwright-core/`, `__cypress/runner/`, `maestro.orchestra.*`, `java.lang.*`, `io.grpc.*`) and storing the normalized app frame in `failure_signatures.top_frame`. The `2` false merges observed on generic assertions (`AssertionError: expected false to be true`, `Error: expect(received).toBe(expected)`) remain inspectable via `top_frame` and can be refined in a future `algo_version = 2` using `merged_into_id` lineage if production data warrants it.

---

## 2. Corpus Composition & Provenance

- **Provenance caveat:** Live production RDS and S3 failure logs were not accessible in this offline workspace. The validation corpus in `apps/server/internal/errorsig/testdata/golden_corpus.json` consists of **14 golden normalizer/fallback unit cases** and **54 curated real-shape Mattermost failure samples** across all four supported frameworks, constructed from TSIO ingest extractor shapes (`apps/server/internal/ingest/{playwright,cypress,detox,maestro}.go`), orchestration markers (`spec-did-not-run`), and Mattermost E2E/mobile failure patterns.
- **Sample breakdown (`54` validation samples, `25` ground-truth root-cause clusters):**
  - **Playwright:** 18 samples (shared infra/browser/selector timeouts, `spec-did-not-run`, `timedOut`, `interrupted`, generic assertion, unique bug, empty error)
  - **Cypress:** 13 samples (`cy.request()` 502s, selector retry timeouts, `timedOut`, `interrupted`, generic assertion, unique bugs, empty error)
  - **Detox:** 12 samples (Detox server connection failures, `by.id(...)` missing views, `timedOut`, generic Jest `expect()`, empty error; all with `ErrorStack == ""`)
  - **Maestro:** 11 samples (`io.grpc.StatusRuntimeException: UNAVAILABLE`, element timeouts, YAML flow frames embedded in `ErrorMessage`, unique layout/badge assertions, empty error)

---

## 3. Quantitative Comparison: `without_top_frame` vs `with_top_frame`

Measured by `TestTop50ValidationComparison` (`go test -race -v ./internal/errorsig/...`):

| Metric | `without_top_frame` (`algo_version=1`) | `with_top_frame` (candidate) |
|---|---:|---:|
| Validation samples | 54 | 54 |
| Ground-truth root-cause clusters | 25 | 25 |
| Distinct signatures produced | **23** | **47** |
| Signatures per failure | **0.426** | **0.870** |
| False splits (clusters split across >1 signature) | **0** | **22** |
| False merges (unrelated clusters sharing 1 signature) | **2** | **0** |
| Total clustering errors (`splits + merges`) | **2** | **22** |

### Top-50 Split / Merge Review

| Cluster Pattern | Samples | Frameworks | `without_top_frame` | `with_top_frame` | Assessment |
|---|---:|---|---|---|---|
| `econnrefused_server` | 4 | Playwright | 1 signature | 4 signatures (+3 splits) | Hashing `top_frame` splits 1 server-down root cause per spec file |
| `api_502_bad_gateway` | 4 | Cypress | 1 signature | 4 signatures (+3 splits) | Hashing `top_frame` splits 1 backend 502 per calling spec |
| `maestro_grpc_unavailable` | 4 | Maestro | 1 signature | 4 signatures (+3 splits) | Hashing `top_frame` splits 1 gRPC outage per YAML flow |
| `detox_server_connect` | 4 | Detox | 1 signature | 4 signatures (+3 splits) | Hashing `top_frame` splits 1 Detox daemon failure per `.e2e.js` |
| `browser_target_closed` | 3 | Playwright | 1 signature | 3 signatures (+2 splits) | Hashing `top_frame` splits 1 browser crash per spec |
| `post_textbox_timeout` | 3 | Playwright | 1 signature | 3 signatures (+2 splits) | Hashing `top_frame` splits across line shifts (`:39` vs `:45`) and specs |
| `cypress_login_input_timeout` | 3 | Cypress | 1 signature | 3 signatures (+2 splits) | Hashing `top_frame` splits across line shifts and specs |
| `detox_channel_list_missing` | 3 | Detox | 1 signature | 3 signatures (+2 splits) | Hashing `top_frame` splits across line shifts and specs |
| `maestro_signin_timeout` | 3 | Maestro | 1 signature | 3 signatures (+2 splits) | Hashing `top_frame` splits across line shifts and YAML flows |
| `generic_assert_channels` vs `generic_assert_threads` | 2 | Playwright, Cypress | Merged (1 sig, +1 merge) | 2 signatures (0 merges) | Generic `AssertionError: expected false to be true` merges without `top_frame` |
| `generic_expect_reply` vs `generic_expect_emoji` | 2 | Detox | Merged (1 sig, +1 merge) | 2 signatures (0 merges) | Generic `Error: expect(received).toBe(expected)` merges without `top_frame` |
| Fallback clusters (`spec-did-not-run`, `timeout`, `interrupted`, `no-error:*`) | 14 | All 4 | 7 signatures (0 splits) | 7 signatures (0 splits) | Empty stack traces leave `top_frame == ""`, identical in both modes |
| Unique single-occurrence failures | 5 | Playwright, Cypress, Maestro | 5 signatures | 5 signatures | Identical in both modes |

---

## 4. Failures Without Usable Error Text by Framework

| Framework | Corpus Samples | Fallback Samples | Fallback Breakdown | Ingest Extraction Behavior |
|---|---:|---:|---|---|
| **Playwright** | 18 | 6 | 3 `spec-did-not-run`, 1 `timeout`, 1 `interrupted`, 1 `no-error:unknown:playwright` | Populates both `ErrorMessage` and `ErrorStack`; orchestration sets `"spec-did-not-run"` when a worker never executes a spec |
| **Cypress** | 13 | 3 | 1 `timeout`, 1 `interrupted`, 1 `no-error:unknown:cypress` | Populates `ErrorMessage` and `ErrorStack` (`estack` / `stack`); hook/runner timeouts can omit error text |
| **Detox** | 12 | 3 | 1 `timeout`, 2 `no-error:unknown:detox` | `ingest/detox.go` joins `failureMessages` into `ErrorMessage` and leaves `ErrorStack = nil`; `ExtractTopFrame` falls back to `ErrorMessage` lines after line 1 |
| **Maestro** | 11 | 2 | 2 `no-error:unknown:maestro` | `ingest/maestro.go` joins JUnit `<failure>`/`<error>` into `ErrorMessage` and leaves `ErrorStack = nil`; XML failures without message/text trigger `no-error:unknown:maestro` |

---

## 5. Normalizer Rules Implemented (`errorsig.NormalizeMessage`)

1. **ANSI stripping:** Removes CSI (`\x1b[...`), OSC (`\x1b]...\x07`), and short 2-byte ESC sequences, then skips blank and pure decorative border lines (`───`, `===`).
2. **Dynamic token replacement:**
   - UUIDs (`8-4-4-4-12` hex) -> `<uuid>`
   - ISO 8601 / log timestamps -> `<ts>`
   - HTTP/HTTPS/WS/WSS URLs -> `<url>`
   - Temp paths (`/tmp/...`, `/private/tmp/...`, `/var/folders/...`, Windows `Temp\...`) -> `<tmp>`
   - CI workspace prefixes (`/home/runner/work/<org>/<repo>/`, `/Users/runner/work/<org>/<repo>/`, `/github/workspace/`) -> `<repo>/`
   - Hex IDs (`0x...`, >=16 hex chars, and 26-char Mattermost alphanumeric IDs containing digits) -> `<hex>`
   - Durations (`ms`, `us`, `ns`, `s`, `m`) -> `<dur>`
   - Standalone numbers -> `<num>`
3. **Stable selector preservation:** Protects Playwright locators (`getByTestId(...)`, `getByRole(...)`, `getByText(...)`, `getByLabel(...)`, `getByPlaceholder(...)`, `locator(...)`), Detox matchers (`by.id(...)`, `by.text(...)`, `by.label(...)`), bracketed attribute selectors (`[data-testid="..."]`), `data-testid`/`data-cy`/`data-test`/`data-qa`/`id` attributes, `:nth-child(N)` / `.nth(N)`, and CSS `#id`/`.class` identifiers before numeric/hex normalization.
4. **Whitespace & UTF-8 cap:** Collapses internal whitespace to single spaces, trims ends, and truncates safely on a UTF-8 rune boundary to at most `500` runes (`SampleMessage` retains the unnormalized stripped first line up to `1000` runes and is never hashed).
5. **Fallbacks:** Emits `spec-did-not-run`, `interrupted`, `timeout`, or `no-error:<class>:<framework>` when no usable error text exists.
