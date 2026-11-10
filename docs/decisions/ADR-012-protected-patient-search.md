# ADR-012: Protected patient search index

## Problem
Patient names are stored as AES-256-GCM ciphertext (ADR-007), so the database
cannot run a normal `LIKE` or `ILIKE` query on them. Users still need to find a
patient by name without decrypting every row in the tenant.

## Alternatives
- Decrypt every patient in the tenant and filter in the application
- Store a plaintext name index (rejected: it defeats encryption)
- Use a separate search cluster (too much operational surface for now)
- Build a keyed blind index with a dedicated HMAC key

## Decision
Every patient name word is normalized (lowercase, letters/digits/hyphen) and
hashed with HMAC-SHA256. The hash input is:

```
v<keyVersion>|<tenantID>|<term>
```

The tenant id is part of the input, so the same name in two tenants produces
different tokens. The tokens live in `patient_search_tokens` with
`(tenant_id, patient_id, token_hash, token_type)` and a lookup index on
`(tenant_id, token_hash)`. The table has RLS and a composite foreign key to
`patients(id, tenant_id)`.

A query is split into at most five terms and at most 100 runes. Every term must
match (AND semantics). Results are capped at 50. The SQL query applies the
assignment filter before rows are decrypted, so an employee cannot search their
way to an unassigned patient. Archived patients are excluded from employee
search but remain searchable for chefs.

`PATIENT_SEARCH_KEY` is a separate 32-byte HMAC key. It must differ from
`PATIENT_ENCRYPTION_KEY`, and both have independent versions for rotation.

## Reasons
- The database never sees a plaintext name or a reversible name index.
- The tenant-bound HMAC prevents cross-tenant correlation of equal names.
- Reusing the AES key would couple two different cryptographic purposes.
- Filtering in SQL keeps unauthorized rows out of the decryption path.

## Consequences
- Creating or renaming a patient deletes and rebuilds its tokens in the same
  transaction as the patient change.
- A name change removes the old tokens, so the old name no longer matches.
- Token rows store a key version so a future rotation can rebuild the index.
- Search audit events record only the number of terms and results, never the
  query itself. The HTTP logger logs the path but not the query string.
- Exact word matching only: there is no fuzzy matching, prefix search or
  substring search on names.

## Risks
- Word-level tokens reveal the number of words in a name to someone who can
  read the token table, but not the words themselves.
- An attacker with the HMAC key and a guessed name can confirm the guess. The
  key therefore needs the same protection as the encryption key.
- Rotating the HMAC key requires rebuilding every token row.

## Open questions
- Should prefix search be supported with a second token set?
- Should search be extended to addresses once those tables exist?
- Which retention period applies to stale token rows after a key rotation?
