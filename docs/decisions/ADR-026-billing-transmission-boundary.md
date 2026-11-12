# ADR-026: Billing transmission boundary

## Status

Accepted. A production payer adapter is not implemented. It needs a
professionally verified § 105 SGB XI or § 302 SGB V procedure first.

## Problem

An approved billing run could only be exported as CSV or PDF. The API reported
`not_configured` for electronic transmission, but there was no place where a
submission would be recorded, retried or audited. A later payer integration
would have had to invent idempotency, retry rules and the audit trail at the
same time as the payer format, which is the riskiest part.

At the same time doc 14 and `docs/compliance/billing-requirements.md` forbid
inventing a § 105 or § 302 format. The concrete procedure, data format, version
and channel must be confirmed with the payer.

## Alternatives

- Keep the read-only `not_configured` endpoint and do nothing until a payer
  procedure is confirmed.
- Implement a best-guess § 105 / § 302 file and send it.
- Build the boundary now: a pluggable adapter interface, an append-only
  transmission table, idempotency and audit, with a synthetic test payer for
  development and the unconfigured adapter in production.

## Decision

### Adapter selection

`billing.Transmitter` has `Name()` and `Submit(ctx, TransmissionRequest)`.
`BILLING_TRANSMISSION_ADAPTER` selects the adapter:

- `unconfigured` (default) refuses every submission. The API answers 503 and no
  transmission row is created. The attempt is still audited as
  `billing.transmission_not_configured`.
- `test` is `billing.TestPayer`, a deterministic in-process payer. It never
  opens a connection. `config.Load` rejects it unless `PLATFORM=dev`.

There is no third value. A real adapter is a code change with its own review.

### API

- `POST /api/billing-runs/{id}/transmit` with `legal_area` and an idempotency
  key (`Idempotency-Key` header, or `idempotency_key` in the body; the header
  wins). Keys are 8 to 128 characters from `A-Z a-z 0-9 . : _ -`.
- `GET /api/billing-runs/{id}/transmissions` lists the attempts, newest first.
- `GET /api/billing-transmission` reports the procedures and the wired adapter.

Only `chef` may submit or list. `employee` and `platform_admin` get 403.

| Outcome | HTTP |
| --- | --- |
| accepted, new attempt | 201 |
| accepted, same key replayed | 200 |
| rejected by the payer | 422, body is the recorded attempt |
| adapter or transport failure | 502, body is the recorded attempt |
| adapter not configured | 503 |
| key reused for another run, or run already has an active or accepted attempt | 409 |
| invalid input, mixed legal areas | 400 |

### Rules

- Only an `approved` run with at least one line can be submitted.
- A run is submitted as one legal area. A run whose lines mix `sgb_xi` and
  `sgb_v` is rejected; the procedures and payers differ.
- The same idempotency key returns the earlier attempt and never calls the
  adapter again, also when that attempt was rejected or failed.
- A run has at most one `pending` or `accepted` attempt. A partial unique index
  enforces this in the database. A rejected or failed attempt allows a
  corrected retry with a new key; the `attempt` counter increases.
- A run with a `pending` or `accepted` attempt cannot be voided. A correction
  must use the payer's procedure.
- A rejected, failed or not-configured outcome is returned as a result, not a Go
  error, so the attempt and its audit events commit. `db.WithTenant` rolls back
  on error.

### Storage

Migration `027_billing_transmission.sql` adds `billing_transmissions`:

- ids, legal area, adapter, status, idempotency key, attempt, payer reference,
  response code and a bounded response message
- `payload_sha256`, a SHA-256 over the run id, report id, version, legal area
  and the sorted line snapshots (ids, quantity, time, code, payer, contract
  reference). It proves what was submitted without storing the claim again.
- no claim payload, patient name or free text
- RLS forced, composite tenant foreign keys, `SELECT, INSERT, UPDATE` for
  `altserv_app` and no `DELETE`
- `clock_timestamp()` defaults, so several attempts in one transaction keep
  their order

### Audit

`billing.transmission_requested`, `billing.transmission_accepted`,
`billing.transmission_rejected`, `billing.transmission_failed` and
`billing.transmission_not_configured`. Metadata holds only ids, the legal area
code, adapter, attempt, status, response code and payer reference. It never
holds the payer message, the procedure text, service names, payer names or the
raw adapter error.

## Reasons

- Idempotency, retry and audit are independent of the payer format. Building
  and testing them now removes that risk from the later payer integration.
- The production default cannot send anything, and the test payer cannot be
  enabled in production by configuration.
- Recording rejected and failed attempts keeps the history traceable for GoBD
  and for support.

## Consequences

- The submission is synchronous inside the tenant transaction. That is safe for
  the in-process test payer only.
- A real network adapter must be idempotent on the idempotency key and should
  move to an outbox and a worker before network I/O is added, so a database
  transaction is not held open across a payer call.
- The response for a rejected or failed attempt carries the recorded attempt,
  so a client can show the response code and retry.

## Risks

- A real payer may need a different status model, for example asynchronous
  acknowledgements. The `pending` status and the `CompleteTransmission` step
  leave room for that, but the model must be reviewed against the confirmed
  procedure.
- A payer may return free text that contains personal data. The service bounds
  the message and keeps it out of audit metadata, but the stored message must be
  reviewed once a real payer is connected.

## Open questions

- Which § 105 / § 302 procedure, format version and channel apply to the first
  tenants?
- Is a clearing house used, and is it a processor under Art. 28 GDPR?
- How are asynchronous payer acknowledgements and corrections delivered?
