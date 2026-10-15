# RET-07 retained pricing source evidence

This fixture is the case-specific runtime evidence for Phase4A-07 at Plan
commit 21035045c425e453bd8f3007619fc0734861012e.

The corrected source generation uses the published exact v1.14.4 producer, creates four
controlled usage events, archives them through the production lifecycle, deletes
all four raw rows, persists the retained projection, changes the
fixture-paid-ret07 context-tier structure through the model-price API, and
reconciles historical costs. The retained pricing source records every deleted
event, its model/provider identity, pricing revision and source, including
manual authority, explicit zero, and unknown/unpriced states.

runtime/ is the macOS replay and platform-replay/linux/ is the pinned Linux
replay. Both include the required new-root backup/restore check. The
clean-checkout-replay.json record binds a detached clean checkout to the
generator commit. sealed-clean-checkout-replay.json separately records Linux
and macOS clean-checkout replay of the downloaded immutable ZIP.

The paid event has 100 normalized input and 10 output tokens. After raw deletion,
the production price API doubles the default rates to 4/16 USD per million and
adds a threshold=50 tier preserving the historical rates 2/8. The strict
`normalized_input_tokens > threshold_tokens` predicate selects this tier for
event 2. Persisted pricing bands and production classification from retained
tokens must both select threshold 50. Historical event cost stays 0.00028;
default-price fallback would produce 0.00056. The historical aggregate remains
0.00235, versus the incorrect default-price aggregate 0.00263.

source-provenance.json pins the corrected bundle `3b3985dc7c26...`, its full
82-member ZIP inventory, release/asset IDs and archive SHA256. The immutable
asset includes the actual database, fixture-only data-key, archive, producer.log,
producer archive, generation logs and original platform backups. Equivalent
generation has a new random key/timestamps and never authenticates these bytes.

historical-original-source.json preserves head 30e48a75's NO-GO and the original
`ce2efcbfea5e...` source identity. Its separately pinned immutable asset preserves
the original database/key/logs byte for byte. That sample did not exercise the
new tier and is not granted semantic qualification by this packaging correction.

Download each `sourceArtifact.archiveName` from its pinned qualification tag
with `gh release download TAG --repo seakee/CPA-Manager-Plus --pattern NAME`.
Run the verifier against both downloaded archives from the repository root:

    python3 -B bin/qualification/verify-ret07-evidence.py \
      --archive /path/to/ret-07-3b3985dc7c26-sealed-evidence.zip \
      --historical-archive /path/to/ret-07-ce2efcbfea5e-sealed-evidence.zip

Without archives the verifier reports metadata-pass / recorded-pass and
originalSourceBytesVerified=false. CI validates the live immutable release,
tag commit and asset metadata before downloading, materializing and replaying
the pinned bytes on Linux/macOS, including a clean detached checkout. The
generation smoke is separate. Windows has no RET-07 runtime qualification.

The evidence deliberately remains runtimeEvidence=pass with casePass=false,
independentAcceptance=pending, UpgradeAuthorized=false, and
TrustedHopReceipt=false.
