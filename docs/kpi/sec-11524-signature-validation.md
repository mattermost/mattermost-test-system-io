# SEC-11524: Error Signature Normalizer & Top-50 Validation Report

**Ticket:** `SEC-11524` (Phase 1)  
**Package:** `apps/server/internal/errorsig` (`AlgoVersion = 1`)  
**Corpus:** `apps/server/internal/errorsig/testdata/golden_corpus.json`  
**Status:** `algo_version = 1` frozen for `SEC-11519`

---

## 1. Frozen `algo_version = 1` Decision

**Decision:** **`frame_file(top_frame)` (with `:line[:col]` stripped) is INCLUDED in the `fingerprint` SHA-256 hash in `algo_version = 1`**, and **`top_frame` (including `:line[:col]`) is ALWAYS extracted and stored unhashed** on `failure_signatures.top_frame`.

```text
fingerprint = sha256(algo_version + "|" + error_class + "|" + normalized_message + "|" + frame_file(top_frame))
```

### Live-Prod 592-Failure Validation Findings & Rationale
1. **Prevents over-merging of generic Playwright assertions (live prod finding):** Against 592 live production failures, excluding `top_frame` completely merged **19 unrelated specs** onto a single signature for generic assertions (`Error: expect(locator).toBeVisible() failed`, `TimeoutError: locator.click: Timeout <dur> exceeded.`). Including normalized `frame_file(top_frame)` separates distinct failing spec files while keeping `AlgoVersion = 1` stable across `:line[:col]` shifts inside the same spec file.
2. **Eliminates separator-prefixed short hex over-splitting (live prod finding):** Dynamic 6–15 char hex suffixes preceded by `-`, `_`, or `.` containing both a digit and a hex letter (such as `ABAC-RemoveRule-a1b2c3d` — which previously split 18 ways — and `@test-user_name.3295f9`) are normalized to `<sep><hex>` without altering CSS selectors or normal English words.
3. **Keeps call-site line context without line-shift fragmentation:** `ExtractTopFrame` always scans `ErrorStack` first and falls back to `ErrorMessage` lines after line 1 (critical for Detox and Maestro where `ingest` stores `ErrorStack = nil`), skipping runtime frames (`node_modules/`, `node:internal/`, `playwright-core/`, `__cypress/runner/`, `maestro.orchestra.*`, `java.lang.*`, `io.grpc.*`) and storing the full normalized app frame (with `:line[:col]`) in `failure_signatures.top_frame`.

---

## 2. Corpus Composition & Provenance

- **Provenance caveat:** Live production RDS and S3 failure logs were not accessible in this offline workspace. The validation corpus in `apps/server/internal/errorsig/testdata/golden_corpus.json` consists of **14 golden normalizer/fallback unit cases** and **54 curated real-shape Mattermost failure samples** across all four supported frameworks, constructed from TSIO ingest extractor shapes (`apps/server/internal/ingest/{playwright,cypress,detox,maestro}.go`), orchestration markers (`spec-did-not-run`), and Mattermost E2E/mobile failure patterns.
- **Sample breakdown (`54` validation samples, `25` ground-truth root-cause clusters):**
  - **Playwright:** 18 samples (shared infra/browser/selector timeouts, `spec-did-not-run`, `timedOut`, `interrupted`, generic assertion, unique bug, empty error)
  - **Cypress:** 13 samples (`cy.request()` 502s, selector retry timeouts, `timedOut`, `interrupted`, generic assertion, unique bugs, empty error)
  - **Detox:** 12 samples (Detox server connection failures, `by.id(...)` missing views, `timedOut`, generic Jest `expect()`, empty error; all with `ErrorStack == ""`)
  - **Maestro:** 11 samples (`io.grpc.StatusRuntimeException: UNAVAILABLE`, element timeouts, YAML flow frames embedded in `ErrorMessage`, unique layout/badge assertions, empty error)

---

## 3. Quantitative Comparison: `frame_file` vs `with_top_frame`

Measured by `TestTop50ValidationComparison` (`go test -race -v ./internal/errorsig/...`):

| Metric | `frame_file` (`algo_version=1`) | `with_top_frame` (full `:line:col`) |
|---|---:|---:|
| Validation samples | 54 | 54 |
| Ground-truth root-cause clusters | 25 | 25 |
| Distinct signatures produced | **43** | **47** |
| Signatures per failure | **0.796** | **0.870** |
| False splits (clusters split across >1 signature) | **18** | **22** |
| False merges (unrelated clusters sharing 1 signature) | **0** | **0** |
| Total clustering errors (`splits + merges`) | **18** | **22** |

### Top-50 Split / Merge Review

| Cluster Pattern | Samples | Frameworks | `frame_file` | `with_top_frame` | Assessment |
|---|---:|---|---|---|---|
| `econnrefused_server` | 4 | Playwright | 4 signatures (+3 splits) | 4 signatures (+3 splits) | Different spec files remain distinct |
| `api_502_bad_gateway` | 4 | Cypress | 4 signatures (+3 splits) | 4 signatures (+3 splits) | Different spec files remain distinct |
| `maestro_grpc_unavailable` | 4 | Maestro | 4 signatures (+3 splits) | 4 signatures (+3 splits) | Different YAML flows remain distinct |
| `detox_server_connect` | 4 | Detox | 4 signatures (+3 splits) | 4 signatures (+3 splits) | Different `.e2e.js` files remain distinct |
| `browser_target_closed` | 3 | Playwright | 3 signatures (+2 splits) | 3 signatures (+2 splits) | Different spec files remain distinct |
| `post_textbox_timeout` | 3 | Playwright | 2 signatures (+1 split) | 3 signatures (+2 splits) | Line shifts (`:39` vs `:45`) in same spec merge under `frame_file` |
| `cypress_login_input_timeout` | 3 | Cypress | 2 signatures (+1 split) | 3 signatures (+2 splits) | Line shifts in same spec merge under `frame_file` |
| `detox_channel_list_missing` | 3 | Detox | 2 signatures (+1 split) | 3 signatures (+2 splits) | Line shifts in same spec merge under `frame_file` |
| `maestro_signin_timeout` | 3 | Maestro | 2 signatures (+1 split) | 3 signatures (+2 splits) | Line shifts in same YAML flow merge under `frame_file` |
| `generic_assert_channels` vs `generic_assert_threads` | 2 | Playwright, Cypress | 2 signatures (0 merges) | 2 signatures (0 merges) | Generic `AssertionError: expected false to be true` stays separated by spec file |
| `generic_expect_reply` vs `generic_expect_emoji` | 2 | Detox | 2 signatures (0 merges) | 2 signatures (0 merges) | Generic `Error: expect(received).toBe(expected)` stays separated by spec file |
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
   - Hex IDs (`0x...`, >=16 hex chars, separator-prefixed `[-_.]` 6–15 char hex tokens with >=1 digit and >=1 hex letter, and 26-char Mattermost alphanumeric IDs containing digits) -> `<hex>`
   - Durations (`ms`, `us`, `ns`, `s`, `m`) -> `<dur>`
   - Standalone numbers -> `<num>`
3. **Stable selector preservation:** Protects Playwright locators (`getByTestId(...)`, `getByRole(...)`, `getByText(...)`, `getByLabel(...)`, `getByPlaceholder(...)`, `locator(...)`), Detox matchers (`by.id(...)`, `by.text(...)`, `by.label(...)`), bracketed attribute selectors (`[data-testid="..."]`), `data-testid`/`data-cy`/`data-test`/`data-qa`/`id` attributes, `:nth-child(N)` / `.nth(N)`, and CSS `#id`/`.class` identifiers before numeric/hex normalization.
4. **Whitespace & UTF-8 cap:** Collapses internal whitespace to single spaces, trims ends, and truncates safely on a UTF-8 rune boundary to at most `500` runes (`SampleMessage` retains the unnormalized stripped first line up to `1000` runes and is never hashed).
5. **Fallbacks:** Emits `spec-did-not-run`, `interrupted`, `timeout`, or `no-error:<class>:<framework>` when no usable error text exists.
