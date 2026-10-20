# Private Fresh S1 acceptance handoff

Implementation base: `aec7a0da2ed6bda960fc80607fc3cd5ea655ed79`, continuing
[Draft #1017](https://github.com/seakee/CPA-Manager-Plus/pull/1017) against
`v2@0a8c6029001872282fcaf07a2b1b377194bce477`.

**Result: PASS for bounded private S1; STOP for Gateway / merge / release pending
independent exact-HEAD acceptance.** These are developer-run observations, not an
independent acceptance decision.

## Reproducible real-process evidence

[`docker.json`](docker.json) is the sanitized output of:

```sh
python3 bin/ci/fresh-s1-docker-proof.py bin/tmp/s1/final --ui
```

Its `sourceSHA256` binds each product implementation file to the tested source;
`containerImages` binds the actual images. The Runtime image was built with the
official opt-in Linux/amd64 loopback target; Manager and Ingress were built with
their repository Dockerfiles. No image was pushed. The official v8.0.13 archive
was also downloaded independently from its GitHub release, and both archive and
extracted executable matched the fixed hashes documented in the
[operation contract](../../fresh-private-s1-key-install.md).

Run: `cpamp-s1-f12c3a04a98e` (see the JSON's authoritative `run` field if comparing
copies). All task-created containers, four volumes and the internal network
were removed after verification. Task build images and ignored local evidence
remain for diagnostics. No existing deployment, user data or Compose project
was used.

| Observation | No Key | Wrong Key | Valid canonical Key |
| --- | --- | --- | --- |
| Empty official CPA, chat status / synthetic upstream arrivals | 200 / 1 | 200 / 1 | 200 / 1 |
| After initial installation, private CPA chat | 401 / 0 | 401 / 0 | 200 / 1 |
| Ingress, before and after installation | 503 / 0 | 503 / 0 | 503 / 0 |

Additional executed negatives and persistence checks:

- Correct Management key **200**; wrong Management key **401**.
- An independent untrusted bridge peer could reach Ingress/503 but not CPA,
  Supervisor, Manager, Ingress control or the synthetic upstream.
- No published container port, privileged container or extra volume/namespace
  consumer; dedicated Manager permit/token mounts were read-only. No Client Key
  write occurred before host proof was issued.
- Missing/wrong Admin **401**; Admin without a permit **409**; submitted
  client proof flags **400**.
- CPA disk contained exactly the one selected secret. Read-only SQLite inspection
  confirmed its canonical identity, encrypted staged secret and nonsecret
  one-shot journal.
- Repeated installation **409** both before and after Manager restart. The CPA
  Key list remained exactly the same one Key; no replace/revoke/clear was sent.
- Runtime restart changed generation and left CPA offline. After read-only
  restoration of the original control route, installation POST, observation GET
  and the existing canonical Strategy each returned **409**. Ingress stayed
  **503**. No generation recovery or retargeting was attempted.

## Browser evidence

Playwright used only a temporary host-loopback relay to the real Ingress. It
logged in using the synthetic Admin, opened the actual canonical Key details,
checked eligibility and clicked **Install this Key once**. The real API/CPA
operation then supplied the bounded effective observation.

- [Desktop, effective window](zh-CN-desktop-effective.png): all four facts and
  exact Runtime generation/window are visible. Installation cannot be repeated.
- [Mobile, expired observation](zh-CN-mobile-expired.png): effective automatically
  becomes unknown/expired without a reload or another write.
- 1280x1000 desktop and 390x844 mobile: no horizontal overflow; normal-page
  console had zero errors/warnings; localStorage/sessionStorage remained empty.
- Light theme: the new status-check button's observed text/background were
  `rgb(15, 23, 42)` / `rgb(246, 248, 251)`. Existing shared secondary-button styling
  elsewhere in the light-theme console was outside this change.

The locale was zh-CN. en, zh-TW and ru strings are present and type checked;
their browser layouts were **not run**. Full Web is reached at `/control`, not
the unsupported trailing-slash `/control/` path.

## Checks and three review passes

- Manager: `go test ./...`.
- Race: `go test -race ./cmd/cpa-manager-plus ./internal/service/cpa ./internal/service/freshkeyapply ./internal/service/freshkeys`.
- Ingress: `go test ./...`.
- Web: 10 Full Web suites / **192 tests**, type check, lint, repository web build
  and Docker packaged Full Web test. Lint had **0 errors** and four existing
  warnings in Accounts files outside this change.
- Windows/amd64 Manager cross-build passed; no native package/release was built.
- Formatting and `git diff --check` passed.

1. **Operation / writes / persistence:** reused the existing canonical encrypted
   Key, moved CPA calls into `service/cpa`, constrained the actual official v8
   seed/projection, and exercised API + UI + real upstream + disk + Manager reload.
2. **Security / concurrency / uncertainty / generation:** fixed repeat attempts
   after an unknown PUT, added durable cross-consumer one-shot arbitration,
   rejected null safety booleans, enforced host lease before PUT and bounded
   live observations, and verified the preserved generation fence. Synthetic
   tests cover uncertain writes, concurrent consumers and effect/config split.
3. **Scope / regression / complexity:** preserved non-Linux compilation with a
   fail-closed platform helper, rejected `null` POST bodies, verified Web/API
   contracts and expiry, and limited the contrast fix to the new status button.
   No new confirmation-worthy issue remained on the final local review.

No second Key authority, new schema, generic config writer, CPA source/fork,
default v7 Compose, Gateway grant, S2, External/Upgrade or release change exists.

## Limits / STOP

The host root/Docker daemon and serial launcher are trusted, not adversarially
attested. Actual dropped-write-reply / power-loss injection was **not run** in
Docker; unknown-result behavior is covered by synthetic transport/consumer tests
and durable-reload/duplicate-write Docker checks. Remote-host and routed IPv6
qualification, malicious host defense and post-generation CPA recovery are not
proven. Upstream arrival counts are application observations, not packet captures.

**Gateway remains Pending/503. Keep PR #1017 Draft and do not merge. Hand the
precise pushed HEAD and these evidence files to ChatGPT for independent review.**
