# ADR-009: No generative AI in billing or care documentation

## Problem
Billing and care reports must be reproducible, explainable and auditable.
Generative models can produce plausible but wrong output and are hard to
reproduce.

## Alternatives
- LLM-generated care reports or billing suggestions
- Statistical/ML models with learned weights
- Explicit rule-based billing and template-driven reports

## Decision
No generative AI and no ML models in the product. Care reports are written by
staff from templates. Billing is computed by explicit, versioned rules over
recorded activities, quantities and contract data.

## Reasons
- Every billed amount and every report line must be explainable to a payer,
  auditor or court.
- Rules can be unit-tested and reproduced for a past date.
- No patient data leaves the system for inference.
- Doc 25 explicitly excludes AI, microservices, Kafka and Kubernetes for the
  first version.

## Consequences
- New billing requirements become rule changes plus tests, not prompt changes.
- Reports stay human-authored; templates only pre-fill activities.
- Rule versions must be stored with the result so historical invoices remain
  reproducible.

## Risks
- Rule maintenance grows with payer diversity.
- Staff may expect auto-completion; the product must communicate the limits.

## Open questions
- Where do rule versions live: code constants, a database table, or both?
- Which payers' rules are in scope for the first release?
