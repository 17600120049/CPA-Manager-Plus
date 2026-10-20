# Fresh Console Manager update channel

Private, nonproduction Fresh New administrators can sign in to the verified
Console, read the saved Manager update channel, choose `auto`, `stable`,
`rc` or `beta`, and explicitly save it. A browser refresh requires Admin
login again. The preference survives Manager restart with the same SQLite data.
An unselected Fresh strategy may use this Manager-only setting before CPA
staging; External and Adoption remain excluded.

## Ownership and API

Manager's existing `updatecheck.Service` owns the preference. The existing
`settings.manager_update_check_v1` JSON record is its only persistent authority.
There is no schema migration, separate settings store, CPA configuration write,
browser storage, or new configuration platform.

Both requests use the verified control prefix's `/api/v2` namespace and existing
Fresh Admin bearer verifier. Responses use `Cache-Control: no-store`.

| Request | Result |
| --- | --- |
| `GET settings/update-channel` | Read SQLite; `{"channel_preference":"auto"}` by default |
| `PUT settings/update-channel`, JSON `{"channel_preference":"stable"}` | 200 only after the existing repository acknowledges persistence |

Invalid enum/body: 400; missing or invalid Admin credential: 401; unsupported
method: 405; wrong content type: 415; unavailable SQLite/update state: 503.
External/Adoption or nonpending Gateway: 409. The endpoint accepts no routing,
Runtime, Client Key or arbitrary settings fields. Failures expose no SQLite
details or credentials.

## Saving and update discovery

Fresh calls `PersistChannel`, which saves without running remote discovery.
The existing Legacy `SetChannel` still saves then immediately checks. Its
old check result therefore does not serve as Fresh's persistence acknowledgement.
An independent background-check failure does not undo a saved preference.

Fresh starts the existing Manager checker with the admitted SQLite handle and
the existing `CPAMP_UPDATE_CHECK_ENABLED=false` opt-out. It selects candidates
using the saved channel, including `auto` following the running version.
The worker retires when Fresh authority leaves the admitted New/Pending slice.
It only discovers update metadata; it never installs software or changes CPA.
The existing shared service mutex serializes checker/save operations; a slow
check can delay a setting request. A 15-second browser timeout produces an
unconfirmed result and bounded read-only recovery, never an automatic write retry.

The save preserves existing update timestamps, errors, index/releases and all
other settings. Failed preference writes invalidate cached preference authority
before a subsequent check/save. GET validates and reads the durable record
without replacing downloaded, unsaved check metadata.

## Browser recovery

The editor holds the last read value and draft in memory. Writes require an
explicit changed selection; a synchronous guard blocks duplicate submission and
refresh/save races. Refresh keeps the rendered value while the read is pending.
Unmount invalidates outstanding response IDs.

A timeout, HTTP error, malformed success or mismatched save reply triggers one
GET of the actual saved channel. No write is repeated. A successful read shows
the current durable value without claiming the original write was acknowledged,
including when another tab has changed it. A failed read keeps editing locked;
the operator can retry GET. Rejected Admin authority clears Console login.
Multiple authenticated tabs use ordinary last committed write semantics; no
cross-tab CAS or transaction coordinator is introduced.

## Safety and evidence

Gateway Pending/503, keyless P0, Runtime generation fences, official CPA/v7/v8
packaging, Ingress authentication, Upgrade, External, Adoption, Plugin Authority
and Release gates are unchanged. Manager-staged Keys remain non-effective.
No gateway forwarding or production support is qualified by this slice.

See [local development acceptance](evidence/fresh-console-update-channel/README.md).
This is developer self-review; the Draft PR still requires independent
exact-HEAD acceptance.
