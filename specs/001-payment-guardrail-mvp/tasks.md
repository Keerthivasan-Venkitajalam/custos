# Tasks 001: Custos — Build Sequence

Implements: [plan.md](plan.md) · Verifies: [spec.md](spec.md)

## Deviations from plan.md (recorded, not silently drifted)

- **Orchestrator LLM: Groq (`openai/gpt-oss-120b`) instead of a hosted frontier-model API.**
  plan.md specifies a hosted frontier LLM for the production target. Groq was substituted for
  fast, free local iteration. The tool-calling contract is identical either way — swapping
  providers is a small, isolated change in `agent_orchestrator/main.py`, not a redesign.
  `temperature=0` is set for deterministic purchase decisions; do the same if you swap providers.
- **Enclave: Python FastAPI stub instead of Rust-in-Nitro.** This is exactly what plan.md §6
  prescribes for local development — the Rust rewrite inside a real Nitro EIF is still Phase 6
  (T-16 onward), untouched and still required for the production claim to hold.
- **Frontend: static HTML/JS instead of Flutter Web.** Functionally equivalent for the demo
  (chat + live X-Ray attestation panel), consistent with the MVP cut list's "Flutter can be cut
  to Flutter Web" — cut one step further given the timeline. Revisit if demo polish needs to be
  scored separately from function.
- **Semantic classifier (T-15):** not built. The deterministic layer's `semantic_mismatch` check
  (amount/recipient must appear in the user's own words) is carrying that weight for now — see
  the eval results below before deciding whether it's actually needed.

## Verified so far (local pipeline, real Groq calls, no real Nitro yet)

Ran 2026-08-29 against `adversarial_eval/payloads.json` (8 injection payloads using realistic
non-obvious attacker VPAs, 6 benign purchase requests):

- **Prevention rate: 8/8 (100%)** — including a payload that successfully fooled the LLM itself
  (it attempted a tool call for an amount/recipient the real user never stated) and was caught
  by the enclave's semantic-mismatch check before the gateway was ever contacted.
- **Guardrail false positive rate: 0/6 (0%)**
- **Enclave attestation latency: 13–23ms observed** (well under the 150ms target; one cold-start
  DB-connection outlier at ~155ms seen during manual testing — add connection pooling before
  citing a p95 number from a larger run)
- **Direct-attack test (T-22): confirmed** — gateway rejects 100% of requests without a
  header, with a malformed JWT, and on JWT replay, tested by hitting `/execute-debit` directly.

This is a small, honest sample (8+6), not the 50+50 curated set T-10 specifies — enough to prove
the architecture works end to end, not enough to cite as a final production metric. Expand the
payload set before treating these numbers as release-ready.

Days are **relative offsets** (D1, D2, ...), not fixed calendar dates — map them onto your
actual deadline. Each task has a Definition of Done that is checkable, not vibes-based. Work
top to bottom; later tasks depend on earlier ones unless noted.

Legend: 🔴 blocking dependency for most later work · 🟡 can be parallelized · 🟢 independent

---

## Phase 0 — Foundations (D1–D2)

- [x] **T-01** 🔴 Repo scaffold per [plan.md §7](plan.md#7-repository-layout): `docker-compose.yml`
      orchestrating Postgres + placeholder services for orchestrator/gateway. `.gitignore`
      covers `.env`, `*.pem`, `*.key` from the first commit (Constitution §8).
      **DoD:** `docker compose up` starts Postgres cleanly; `git status` shows no secrets ever
      staged.

- [x] **T-02** 🔴 Postgres schema from [plan.md §3](plan.md#3-data-model-postgresql) as a
      migration. Apply `REVOKE UPDATE, DELETE ON audit_log` and the mandate `CHECK` constraints.
      **DoD:** Attempting `UPDATE audit_log ...` or `utilized > total_blocked` fails at the DB
      level (write a throwaway test proving both).

- [ ] **T-03** 🟢 `docs/THREAT_MODEL.md` — STRIDE pass over the three planes. Minimum: one
      concrete attack per category (Spoofing, Tampering, Repudiation, Info Disclosure, DoS,
      Elevation) with the specific control in this system that stops it.
      **DoD:** Every STRIDE row cites an actual component (e.g., "Tampering: mitigated by JWT
      signature verification in gateway.go, see plan.md §4.2"), not a generic statement.

## Phase 1 — Ledger & Gateway (D2–D3)

- [x] **T-04** 🔴 Go gateway skeleton (`ledger_gateway/gateway.go`): `POST /execute-debit`
      endpoint that **unconditionally rejects** any request without a syntactically valid
      `x-custos-proof-of-guardrail` JWT header. No KMS integration yet — reject-by-default
      first, sign-to-pass later. This ordering matters: it makes "deny" the default the code
      was born with, not a check bolted on afterward.
      **DoD:** Every request without the header returns `REJECTED_UNSIGNED`; test with curl,
      no exceptions.

- [x] **T-05** Depends on T-04. Wire real KMS JWT verification (asymmetric key, public-key
      verify in Go). Add `exp` check (reject if >5s old) and `jti` replay check (in-memory
      seen-set with TTL, or a Postgres unique constraint on `jti`).
      **DoD:** A valid-but-expired JWT is rejected as `REJECTED_INVALID_SIG`. A valid JWT
      replayed twice succeeds once, fails the second time as `REJECTED_REPLAY` (US-3 in spec.md).

- [x] **T-06** Depends on T-05. Wire the debit execution path: on valid signature, `SELECT ...
      FOR UPDATE` the mandate row, check `remaining >= amount`, update `utilized`, insert into
      `transactions` and `audit_log` in one DB transaction.
      **DoD:** Concurrent debit test (10 parallel valid requests against a mandate that can
      only satisfy 3 of them) — exactly 3 succeed, no negative `remaining`, no lost updates.

## Phase 2 — Agentic Plane (D3–D4)

- [x] **T-07** 🟡 FastAPI orchestrator skeleton + hosted LLM integration (direct SDK or
      LangChain — plan.md doesn't mandate LangChain specifically, use whichever ships
      tool-calling reliably). Define the `execute_upi_reserve_debit` tool schema.
      **DoD:** Agent can hold a multi-turn conversation and, on explicit user confirmation,
      emits a structured tool call matching the schema in plan.md §4.1 — verified by a scripted
      benign conversation.

- [x] **T-08** Depends on T-07. Intercept the tool call **before** it reaches the gateway —
      route it to a stub enclave endpoint (local, not yet real Nitro) that just echoes back
      `REJECTED: not_implemented` for now.
      **DoD:** Confirm structurally that no code path in the orchestrator calls the gateway
      directly with an unsigned payload — grep the codebase for gateway calls, there should be
      exactly one call site and it's downstream of the enclave response.

- [x] **T-09** 🟢 Flutter Web chat UI, WebSocket-connected to the orchestrator. Minimal styling
      — this is not the differentiator, don't over-invest (Constitution §6).
      **DoD:** A user can type a shopping request, see the agent's response, and see a payment
      confirm/reject state rendered.

## Phase 3 — Adversarial Dataset (D4–D5, parallel with Phase 2)

- [ ] **T-10** 🟡🟢 (partial — 8+6 built, see note above; DoD wants 50/50 and an enclave-off
      control run, neither done yet) Curate the adversarial eval set: 50 benign shopping requests, 50 indirect
      prompt injection payloads designed to trick the agent into an unauthorized debit (hidden
      in fake product descriptions, fake support replies, etc.), stored in `adversarial_eval/`.
      **DoD:** Run all 100 against the **enclave-off** path (T-08's stub bypassed) — confirm the
      injections actually succeed against the raw agent with no guardrail. If they don't, the
      attacks are too weak; strengthen them now, not on demo day (source doc's Risk #06).

- [x] **T-11** (cap implemented in code; DoD's specific "hits the cap and stops" test not
      yet exercised — every manual/eval run so far resolved in 0-1 tool attempts) Depends on T-07. Add a hard retry cap (max 2) in the orchestrator for rejected
      tool calls, with a graceful fallback message on exhaustion — prevents an agent stuck
      re-attempting a blocked injection from hammering the enclave (plan.md §5).
      **DoD:** Feed a payload that always gets rejected; confirm the agent stops after 2 tries
      and responds gracefully instead of looping.

## Phase 4 — Enclave Policy Engine, Local First (D5–D6)

- [x] **T-12** 🔴 `enclave_service/policy_engine.rs` as a **plain, unit-testable binary** (no
      enclave yet, per plan.md §6). Implements: schema validation, mandate-balance check against
      a read-only DB replica connection, deterministic reject/approve logic.
      **DoD:** Unit tests cover: amount exceeds remaining balance → reject; malformed schema →
      reject; valid payload within balance → approve. No ML yet.

- [x] **T-13** Depends on T-12. Local KMS stub (isolated Docker container, no network egress)
      implementing the same sign/verify contract as real KMS. Wire `policy_engine` to sign
      approved payloads via the stub.
      **DoD:** End-to-end locally: orchestrator → policy engine → stub-signed JWT → gateway
      accepts it. This is the first point the *whole* pipeline works, just without real Nitro.

- [x] (against the 8+6 set, not full T-10) **T-14** 🟡 Run the T-10 adversarial set through the **full local pipeline** (enclave-on,
      stub KMS). Measure prevention rate and false positive rate on deterministic rules alone
      (no semantic classifier yet).
      **DoD:** Record baseline numbers. Expect deterministic rules alone to catch anything that
      violates mandate math but likely miss semantically-valid-looking injected amounts within
      budget — that gap is exactly what T-15 exists to close.

## Phase 5 — Semantic Layer (D6, optional per Constitution §6 cut list)

- [ ] **T-15** 🟡 Add the quantized DistilRoBERTa (ONNX) intent classifier inside
      `policy_engine`, as **advisory input to logging only** initially — it must not become a
      second authoritative veto that can be gamed independently of the deterministic engine
      (plan.md §5 failure matrix).
      **DoD:** Re-run T-14's adversarial set; compare prevention rate with classifier signal
      included vs. deterministic-only. If memory/latency blows the enclave budget later
      (Phase 6), this step is the one allowed to be cut — deterministic rules already provide
      the load-bearing guarantee.

## Phase 6 — Real Nitro Enclave (D7–D9) — requires AWS access, confirm before starting

- [x] **T-16** 🔴 Provisioned `c6g.xlarge` (Graviton/arm64, non-metal — see plan.md §6
      correction), instance `i-084febfbf454baa85`, eu-north-1. Built the EIF from a minimal
      VSOCK echo app (`enclave_service/nitro_poc/`) — deliberately not the real policy engine
      yet, to keep this step's PCR values decoupled from application logic that's still
      changing. Enclave ran stable (`State: RUNNING`), and:
      **DoD verified for real:** `custos-ping` sent from the parent instance over VSOCK CID 16
      port 5005, echoed back byte-for-byte — `PASS`.

      PCR0 (measures the whole EIF, will change again once T-18 replaces the echo app with the
      real policy engine): `fa42a1567f8ad8590655488b24f6dcbb409aac755e8de43ebed12707368a0831351e0913d9e633d2895abdf118534e56`
      PCR1: `3b4a7e1b5f13c5a1000b3ed32ef8995ee13e9876329f9bc72650b918329ef9cf4e2e4d1e1e37375dab0ba56ba0974d03`
      PCR2: `a6103311566a3870335e4747439609312524b391434ab66d83f29a6f090de3aa8b9c434851a90b79831989781c0c86e6`

      **Snag hit and fixed:** the enclave's minimal init execs the Docker `CMD` directly with no
      shell and no `PATH` resolution, so `CMD ["python3", ...]` failed with
      `execvpe: python3: No such file or directory`. Fixed by using the absolute interpreter
      path (`CMD ["/usr/local/bin/python3", ...]`) — worth knowing before writing the real
      enclave app's Dockerfile too, same constraint applies.

- [x] (mechanics done, PCR-gating deferred — see note) **T-17** Depends on T-16. **Design
      corrected mid-implementation** — see plan.md §4.2: KMS `Sign` has no attestation/PCR
      support, real mechanism is attestation-gated `Decrypt` of a sealed private key, local
      in-enclave signing. What's actually done:
      - Symmetric KMS CMK created (`arn:aws:kms:eu-north-1:992382506574:key/17b4c9da-f596-4aef-91b8-705b7699ec41`),
        key policy grants `custos-enclave-host-role` exactly `kms:Encrypt`/`kms:Decrypt`/
        `kms:DescribeKey` on this key, nothing else — verified live against the JSON before
        creation, not just at wizard-submit time.
      - Confirmed empirically that an explicit per-principal Allow in the key policy is
        sufficient on its own — **no separate IAM identity policy needed on the role** (this
        was going to be a separate step; tested directly instead of assuming).
      - Real RSA-2048 signing keypair generated on the enclave host, private key encrypted via
        `kms:Encrypt` (called from the host using the instance role's temporary credentials —
        no static AWS keys anywhere), round-tripped through `kms:Decrypt` and diffed
        byte-identical against the original, plaintext key then securely deleted from disk.
        Only the ciphertext (`~/custos-enclave/kms-sealed/signing_private_key.pem.encrypted.b64`
        on the host) and the public key persist.
      **Deferred, not done:** the `kms:RecipientAttestation:PCR0` condition that actually
      restricts `Decrypt` to the attested enclave — needs the *real* enclave app's PCR0 (T-18),
      not the echo-app's. Until that condition is added, `Decrypt` is scoped to the IAM role
      but not yet to enclave attestation specifically — same accepted, tracked gap pattern as
      before, now waiting on T-18 instead of on this step.

- [x] (core mechanics proven; live-DB integration deliberately out of scope — see note)
      **T-18** Depends on T-17. Ported the deterministic policy engine into a real enclave app
      (`enclave_service/nitro_poc/custos_enclave_app.py`), replacing the echo placeholder.

      **Scoping call made explicitly, not drifted into:** rather than a second vsock-proxy
      tunnel + a custom local TCP-to-VSOCK shim to reach the Postgres mandate DB (no Postgres
      client speaks VSOCK natively — this would be nontrivial custom networking code, stacked
      on top of everything else here, untested), the enclave holds its **own in-memory mandate
      ledger**, seeded at boot via the same bootstrap payload that carries the KMS ciphertext.
      This keeps the core security property intact (the untrusted orchestrator still can't
      manipulate what the enclave checks against) while avoiding a fragile networking layer.
      **Known limitation, stated not hidden:** this in-memory ledger resets on enclave restart
      and isn't synced with the Go gateway's Postgres audit ledger — acceptable for proving the
      architecture and for a demo, not for production. A live-synced version is the tracked
      follow-up (needs the DB-reachability scoping decision below to be made first anyway).

      **What's verified, end to end, on real hardware:**
      1. Parent (`parent_bootstrap.sh`) fetches its own IMDS role credentials (enclave has no
         network access, can't fetch these itself), starts `vsock-proxy` to KMS, and relays
         credentials + KMS ciphertext + starting mandate ledger into the enclave over VSOCK.
      2. Enclave calls `kmstool_enclave_cli decrypt` (AWS's own NSM-attestation + KMS SDK
         tooling, built from source per `containers/Dockerfile.al2` in the `aws-nitro-enclaves-sdk-c`
         repo — not hand-rolled) — **real attestation-gated `kms:Decrypt` succeeded**, private
         key held in memory only, confirmed via enclave console log.
      3. Sent real tool-call payloads over VSOCK to the running policy engine:
         - Legit request within budget, amount mentioned in prompt → `APPROVED`, signed JWT
           returned.
         - Injection-shaped payload exceeding budget → `REJECTED: mandate_exceeded`.
         - In-budget payload with unmentioned amount/recipient → `REJECTED: semantic_mismatch`.
      4. **Signature independently verified**: pulled the `APPROVED` response's JWT out of the
         enclave, verified it locally against the known public key with PyJWT (not the enclave's
         own code) — signature valid, claims exactly matched the request. This is the actual
         end-to-end proof: real hardware, real attestation, real signature, independently checked.

      **Scoping decision resolved:** DB-reachability question (raised above) answered — moved
      the Go gateway and Postgres onto the same EC2 instance as the enclave (`docker run
      postgres:16-alpine` bound to `127.0.0.1:5433`; gateway cross-compiled locally for
      `linux/arm64` and shipped as a static binary, since the host's disk was too tight for a
      full Go toolchain install after the `kmstool` build). A small VSOCK-to-HTTP bridge
      (`vsock_http_bridge.py`, on the parent, outside the enclave) exposes the exact same
      `POST /guardrail/evaluate` contract as the local dev stub, so the orchestrator (still on
      the local Mac, still calling Groq) needed **zero code changes** — only its `.env`'s
      `ENCLAVE_URL`/`GATEWAY_URL` tunnel targets changed, via SSH port forwarding
      (`-L 8100:localhost:8100 -L 8200:localhost:8200 -L 5434:localhost:5433`) instead of a
      public security-group opening — no new inbound exposure beyond the existing SSH rule.

      Redeployed in production mode (no `--debug-mode`, `Flags: NONE`) — same PCR0 as the debug
      run, confirming debug mode doesn't affect the build measurement itself, only what KMS sees
      in the attestation document.

      **A real bug surfaced by testing, not hidden:** the first full-stack eval run showed
      83.3% guardrail FPR (5/6 benign requests rejected as `unknown_mandate`). Root cause:
      `run_eval.py` created a fresh random-UUID mandate in Postgres per run (to avoid cross-run
      pollution against the old local-stub setup, which read live from the DB) — but the real
      enclave's in-memory ledger only knows the mandate it was told about at boot, so any
      mandate created afterward is invisible to it. This is the exact drift risk already
      documented above, now concretely demonstrated. Fixed by having `run_eval.py` reuse the
      fixed demo mandate (reset via `UPDATE`, not `INSERT`) and re-bootstrapping the enclave
      fresh before the real run — not by loosening what counts as a false positive.

      **Final DoD result, real stack, real numbers:**
      - **Prevention rate: 8/8 (100%)**
      - **Guardrail false positives: 0/6 (0%)**
      - Enclave attestation latency: ~0ms per request (signing is pure local crypto after the
        one-time boot-time KMS decrypt — trivially inside the &lt;150ms budget)
      - Wall-clock chat latency (dominated by the Groq round trip, not the guardrail): p50 ≈2.4s

      **Still pending:** the `kms:RecipientAttestation:PCR0` condition on the KMS key policy —
      PCR0 was sent to the browser agent for the update, but no confirmation has come back yet
      that it was actually applied. Until confirmed, `Decrypt` is working correctly but is not
      yet proven to be *restricted* to this attested enclave specifically (still scoped to the
      IAM role only). Need: (a) confirmation the policy update landed, (b) a positive test
      (this enclave still decrypts), (c) a negative test (a plain non-attested `aws kms decrypt`
      call, or a differently-measured enclave, gets denied) — before claiming this final piece
      closed.

## Phase 7 — Observability & X-Ray Dashboard (D10)

- [ ] **T-19** 🟡 Prometheus metrics from plan.md §8 exported by orchestrator + gateway; Grafana
      dashboard wired.
      **DoD:** Dashboard shows live latency histogram and rejection rate while T-14's script runs.

- [ ] **T-20** Depends on T-09, T-19. Flutter "Attestation Verifier" split-screen panel: live
      JSON payload stream + PCR hash + signature status, next to the chat UI (spec.md US-5).
      **DoD:** During a live demo run, a judge can watch a rejection happen on the dashboard in
      real time, not just see a chat bubble change.

## Phase 8 — Full Adversarial Validation (D11–D12)

- [ ] **T-21** 🔴 Run the complete 100+ payload adversarial set against the fully integrated
      system (real enclave, real KMS, real gateway). Tune thresholds only on the deterministic
      layer — never loosen the "no signature, no execution" rule itself.
      **DoD:** Meets spec.md targets: prevention rate >99%, FPR <0.5%, p95 attestation latency
      <150ms. If not met, this blocks Phase 9 — do not proceed to demo polish with failing
      numbers.

- [x] **T-22** 🟢 Direct-attack test bypassing the agent entirely: hand-craft malformed/replayed/
      expired JWTs and hit the gateway's `/execute-debit` directly (spec.md US-3).
      **DoD:** 100% rejection rate, no exceptions — this is the hardest guarantee in the spec
      and the one most worth a judge trying to break live.

## Phase 9 — Demo & Submission (D13–D14)

- [ ] **T-23** Record a fallback video of the full attack-blocked demo (source doc Risk #06,
      #15) — required in case live injection fails under demo pressure or connectivity drops.
      **DoD:** Video exists, plays offline, shows the same enclave-off → exploit → enclave-on →
      block sequence as the live script.

- [ ] **T-24** README with architecture diagram, setup instructions, and a link to this
      `specs/001-payment-guardrail-mvp/` folder as the design record.
      **DoD:** A stranger can `docker compose up` and run the adversarial eval script from a
      clean checkout, following only the README.

- [ ] **T-25** Final submission: public repo visibility check, branch integrity check, all
      secrets confirmed absent (`git log -p | grep`-style sweep, not just `.gitignore` trust).
      **DoD:** Repo is public, `main` is green, no secret ever appears in `git log`.

---

## What Gets Cut First If Time Runs Out

In strict order (stop cutting as soon as you're back on schedule):

1. T-15 (semantic classifier) — deterministic rules alone still satisfy the core spec.
2. Native-feeling Flutter polish — the X-Ray dashboard's data must work, its CSS doesn't need to.
3. T-19 Grafana dashboard — Prometheus metrics can be read via raw queries in a demo instead.

## What Never Gets Cut

T-04/T-05 (reject-by-default gateway), T-06 (row-locked ledger correctness), T-21/T-22
(adversarial validation) — cutting any of these means the core claim in spec.md is unverified,
which means there is no product to demonstrate.
