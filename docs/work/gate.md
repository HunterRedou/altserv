# Pilot gate

Do not use real patient data until every line is checked.

## Code and operations

- [ ] WP-00 to WP-19 are done, or a decision records why a package is not
      necessary for the pilot.
- [ ] WP-20 is done for every screen in the pilot scope (D-6).
- [ ] One PITR drill record exists in `docs/drills/` (WP-10).
- [ ] The backup alarm fired once in a test (WP-11).
- [ ] A staging load test result is recorded (WP-12).
- [ ] The audit sink receives events (WP-14).
- [ ] Production keys come from the secret manager, not from a file (WP-09).
- [ ] The app does not start in prod without DB TLS, `METRICS_TOKEN` and HSTS
      (WP-01 to WP-03).

## Reviews

- [ ] E-1 to E-6 are signed in `docs/compliance/review-record.md`.
- [ ] The penetration test has no open critical or high finding.

## Decisions

- [ ] D-1 to D-7 have an answer in `docs/work/decisions.md`.

## Sign-off

| Role | Name | Date |
| --- | --- | --- |
| Owner | | |
| Controller (care service) | | |
