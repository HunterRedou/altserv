# ADR-007: Field-level encryption for patient PII

## Problem
Patient names and birth dates are special-category health data. A database
dump, a backup, or an over-broad read must not expose them in plaintext.

## Alternatives
- Store plaintext and rely on access control only
- Encrypt the whole database / disk (TDE)
- Encrypt the whole row as one blob
- Encrypt each PII field with AES-256-GCM

## Decision
Encrypt patient PII fields individually with AES-256-GCM before they reach
PostgreSQL. `patients` stores `first_name_enc`, `last_name_enc` and
`birth_date_enc` as `BYTEA`, plus the `key_version` used for that row.

Every encryption uses a fresh random 96-bit nonce. The stored value is
`nonce || ciphertext || tag`. The additional authenticated data binds the
ciphertext to its context:

```
v<keyVersion>|<tenant_id>|<patient_id>|<field>
```

The key is supplied as base64 `PATIENT_ENCRYPTION_KEY` (32 bytes) and is never
committed. `PATIENT_KEY_VERSION` selects the active key version.

## Reasons
- A leaked database or backup contains no readable patient names.
- AEAD detects tampering and prevents moving a ciphertext to another patient,
  tenant or field (the AAD no longer matches).
- Per-field encryption keeps list/detail queries and future search indexes
  possible without decrypting unrelated columns.
- A key version per row allows staged key rotation.

## Consequences
- The application must decrypt before returning data; the API never returns
  ciphertext.
- Equality search and sorting on encrypted fields are not possible directly.
  Searchable derived values (e.g. blind indexes) would be a separate decision.
- Losing the key means losing the data. Key backup and KMS/secret-manager
  storage are mandatory in production.
- The service refuses rows whose `key_version` it cannot handle.

## Risks
- The key currently lives in the environment. A host compromise exposes it.
- Encryption does not replace RLS, audit logging or least privilege.
- Key rotation tooling (re-encrypt on read/write) is still open.

## Open questions
- Which KMS/secret manager is used in production?
- Do we need a blind index for patient search?
- Should audit metadata ever contain derived (non-PII) patient attributes?
