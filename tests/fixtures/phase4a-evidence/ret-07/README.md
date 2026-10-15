# RET-07 retained pricing source evidence

This fixture is the case-specific runtime evidence for Phase4A-07 at Plan
commit 21035045c425e453bd8f3007619fc0734861012e.

The source generation uses the published exact v1.14.4 producer, creates four
controlled usage events, archives them through the production lifecycle, deletes
all four raw rows, persists the retained projection, changes the
fixture-paid-ret07 context-tier structure through the model-price API, and
reconciles historical costs. The retained pricing source records every deleted
event, its model/provider identity, pricing revision and source, including
manual authority, explicit zero, and unknown/unpriced states.

runtime/ is the macOS replay and platform-replay/linux/ is the pinned Linux
replay. Both include the required new-root backup/restore check. The
clean-checkout-replay.json record binds a detached clean checkout to the
generator commit.

Run the machine-checkable verifier from the repository root:

    python3 bin/qualification/verify-ret07-evidence.py

The evidence deliberately remains runtimeEvidence=pass with casePass=false,
independentAcceptance=pending, UpgradeAuthorized=false, and
TrustedHopReceipt=false.
