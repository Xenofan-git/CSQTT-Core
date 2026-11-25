# DeployManager — offline lifecycle acceptance

This is a verification aid, not an installer. It accepts a locally saved JSON report
and checks completeness and consistency. It cannot prove the report's authenticity,
authorize an operation, or replace independent observation. It does not change the
pinned helper, installer, or inventory-only mode.

Run from the repository:

    python3 adapt/test-csqtt-deploy-evidence.py
    python3 adapt/verify-csqtt-deploy-evidence.py /path/to/collected-report.json

Schema 1 requires isolated-lab scope, base and executed installer SHA-256, candidate
SHA-256, observed controller -> manager -> actual SSH chain, and independent
postcheck. For rollback the reviewed fault-injection copy must have its own distinct
SHA. Process outcome must record the actual exit code, timeout/signal flags and
structured markers. Before/after observations include ARM64, unchanged default route,
SSH, unaffected legacy Android runtime, service, ports, TUN and SQLite. A successful
install additionally requires the pinned installed binary and HTTPS panel health.
Uninstall requires actual runtime absence, released ports and preserved persistent
state. Rollback requires a post-cutover failure, nonzero failure code (not a refusal),
automatic restoration, original hashes of all five runtime components and healthy
restored service. See the synthetic fixture in the test file for the precise field
names. No real credentials, secret URL, or log body is required.

Exit 0 means EVIDENCE_CONSISTENT, not release authorization. Exit 1 means
NOT_ACCEPTED; exit 2 means unreadable evidence. Missing fields are not inferred.

## Prior evidence audit

Source: CSQTT-DEPLOY-MANAGER-LAB-TEST-2026-11-25.md and Amsterdam journals.
These are historical reports, not a newly executed lifecycle:

| Operation | Established | Still missing for full chain |
|---|---|---|
| Install | Standalone isolated ARM64 installer, candidate running and panel healthy | Controller -> real manager -> actual SSH install |
| Uninstall | Removal observed; baseline independently restored | Successful wrapper exit and intermediate absence/port checks; full controller chain |
| Rollback | Test-only post-cutover fault, old five hashes restored, service and panel healthy | Actual first invocation exit code; full controller chain |
| Controller preflight | Actual ARM64 helper, authenticated API, simulated SSH | Real remote lifecycle, not just exit 73 |

Previously blocked staging and remote operations are not repeated or circumvented.
No production promotion is implied by the synthetic test results.
