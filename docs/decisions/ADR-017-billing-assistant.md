# ADR-017: Rule-based billing assistant

## Problem
Doc 14 requires a billing assistant that turns confirmed activities into
checkable billing proposals. It must use the valid contract, the service
catalog and deterministic rules. A chef must approve the result. The assistant
must not use generative AI, must detect invalid services, missing
prerequisites, exclusions, combination problems and double billing, and must
check the report status. PDF and CSV are internal only until the concrete
SGB XI §105 and SGB V §302 submission procedures and their current technical
rules are professionally clarified. Health data must never be sent by
unprotected standard email.

## Alternatives
- Let an LLM or a learned model propose billing lines
- Bill directly from `report_activities` without a stored proposal and approval
- Compute lines at export time and keep no rule result
- Store only a final invoice and no findings
- Use one global rule set for every tenant and payer
- Send the export to payers by email as a first integration
- Keep the billing tables without a version column and rely on row locks
- Cascade-delete billing lines when a report activity is corrected

## Decision
Billing is a deterministic engine over explicit data:

- A `billing_run` is one evaluation of one `ready_for_billing` report. The same
  report, contract state and rules always produce the same lines and findings.
  There is no randomness and no model.
- `status` is `draft`, `approved` or `voided`. A run is generated as `draft`,
  approved by a chef, or voided. A voided run stays as history.
- `billing_lines` and `billing_findings` are stored, so an approval and a later
  export refer to a fixed, inspectable result.
- `version` supports optimistic locking. `Generate` needs version `0` for the
  first run and the current draft version to regenerate. `Approve` and `Void`
  need the current version. A stale write gets `409`.
- An approved run cannot be regenerated. It must be voided first. Voiding
  deletes its lines and findings so the performed activities can be billed
  again by a corrected run.

The engine checks, in order:

- the report is `ready_for_billing` and is not empty
- each activity is `recorded` and appears only once
- each activity has a service definition and a contract
- the service definition and the contract are valid at the performed time,
  compared in the engine against the performed timestamp rather than against
  the generation time
- the service and contract legal areas match
- the activity is not already billed in another run
- the same service appears more than once (info only)

Findings have three severities:

- `error` blocks approval and usually blocks the line
- `warning` is shown but does not block
- `info` is shown but does not block

Tenant rules in `billing_rules` add `exclusion`, `prerequisite` and
`combination` checks. A rule names two different service definitions, a legal
area and a validity range. Both definitions must exist and match the rule's
legal area. A rule outside its validity range does not apply. Generic checks
stay in code; only tenant-specific structure is configuration.

Billing lines keep snapshots of the service code, service name, legal area,
payer name and contract reference. A later catalog or contract change cannot
rewrite an approved run.

The report-activity link is protected:

- `billing_lines_report_activity_fk` uses `ON DELETE RESTRICT`, not `CASCADE`.
  A report correction cannot silently delete a line of an approved billing run.
- The report service maps a foreign-key violation to `409` with the message
  "an approved billing run references this report; void the billing run first".
- The chef must void the run, correct the report and generate a new run.

Exports are internal only:

- CSV uses `;` as separator and prefixes cells that start with `=`, `+`, `-`
  or `@` with `'` so a spreadsheet cannot execute them as formulas.
- PDF is a small PDF 1.4 document with the built-in Helvetica font and WinAnsi
  encoding. It needs no external PDF library or font file.
- Both exports contain ids and billing data, not patient names or free text.
- Both exports require an `approved` run and at least one line. An export writes
  an audit event.

Transmission is explicitly not implemented:

- `GET /api/billing-transmission` returns status `not_configured` and the two
  informational procedures `SGB XI §105` and `SGB V §302`.
- `UnconfiguredTransmitter.Submit` always returns `ErrNotConfigured`.
- No email path exists. A later implementation must use a verified procedure
  and a secure transport after professional clarification.

Access and audit:

- Only a `chef` may generate, read, approve, void, export or manage rules.
- An `employee` and a `platform_admin` get `403`. A platform admin gets no
  automatic tenant access (ADR-004 and the platform-admin reminder). Future
  break-glass access must be explicit, time-limited and audited.
- Every generate, approve, void, export and rule creation writes an audit event.
  A denied request writes an `access.denied` event when a tenant is known.
- The run, its lines, its findings, the status change and the audit event are
  written in one tenant transaction.

## Reasons
- Explicit rules are reproducible for a past date and explainable to a payer,
  an auditor or a court (ADR-009).
- A stored run with stored findings makes an approval reviewable instead of
  recomputing a possibly different result later.
- Snapshots keep historical billing data stable while catalogs and contracts
  evolve.
- Chef-only access matches doc 14 and the existing approval model for reports.
- Internal-only exports avoid inventing an unverified payer interface and avoid
  an unsafe email path for health data.
- `ON DELETE RESTRICT` turns a silent data loss into a clear conflict that the
  chef can resolve by voiding the run.
- Optimistic locking gives concurrent users a clear `409` instead of an
  overwritten approval.

## Consequences
- A report correction after approval needs an explicit void first.
- An activity can be billed by only one active run at a time. The unique
  `(tenant_id, activity_id)` on `billing_lines` enforces this at the database.
- A voided run keeps its header and audit history but no lines or findings.
- Regenerating a draft replaces its lines and findings, so the version changes
  on every evaluation.
- The client must send the current version on every generate, approve and void.
- A new payer rule is a row plus tests, not a prompt change.
- CSV and PDF can be handed to internal staff, but they are not a legal
  submission to a payer.

## Risks
- The generic engine does not know every payer-specific rule. Tenants must add
  the rules they need, and a missing rule can produce an incomplete proposal.
- Rule rows are append-only versions in practice, but the first version has no
  edit or delete endpoint. A wrong rule needs a new row with a later validity
  range or a manual correction.
- The PDF uses a fixed layout and WinAnsi text. Names outside WinAnsi are
  transliterated or replaced, and very long values are truncated.
- The exports contain billing data, so access control and audit matter more
  than the file format.
- Transmission remains open. A real integration needs a verified procedure,
  certificates, test cases and a data-protection review.
- A rule's validity is checked against the performed time of each activity, not
  against the generation time. A rule added later can still apply to an older
  activity if its validity range covers the performed date.

## Open questions
- Which concrete SGB XI §105 and SGB V §302 procedures and technical rules are
  in scope, and which certification is required?
- Do billing runs need a frozen copy of the contract and catalog version ids, in
  addition to the text snapshots?
- Should a warning require an explicit acknowledgement by the chef before
  approval?
- Should a tenant rule be editable with a new version, or is append-only with a
  validity range enough?
- Is a reversal document needed for a voided approved run, or is the void event
  and audit trail sufficient?
- Which roles besides `chef` may read an approved run for internal controlling,
  and does that need a separate permission?
