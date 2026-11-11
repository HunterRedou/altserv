# ADR-020: Authentication hardening

## Problem

The access-control policy listed four authentication gaps that blocked a
production launch:

- No multi-factor authentication for `chef` and `platform_admin`.
- No password reset flow.
- No way to disable a user account.
- No scheduled purge of expired sessions and tokens.

The system had a strong password hash, a hashed session token, CSRF protection
and a per-process login limit. It still had one factor. A stolen password was
enough for a `chef` to read every patient in the tenant. A lost password needed
manual database work. A disabled employee kept a valid session until it expired.
Expired rows accumulated without limit.

The step had to add these controls without creating a hidden superuser path and
without putting health data into audit metadata.

## Alternatives

- Add a third-party identity provider for SSO and MFA.
- Store the TOTP seed in plaintext and rely on database access control.
- Use SMS or email one-time codes.
- Require MFA for every role, including `employee`.
- Send reset links by email in this step.
- Delete expired rows with a normal application query.
- Rely on session expiry alone instead of an explicit disable action.

## Decision

### MFA scope and flow

- MFA is required for `chef` and `platform_admin`. `employee` accounts may log in
  with one factor. The rule is `mfaRequired(role)`, so a future role must be
  added explicitly.
- A privileged login with MFA enabled returns `202` and a challenge. The
  response sets no session cookie. A session exists only after the second factor
  is accepted.
- A privileged login without MFA returns a normal session plus
  `mfa_enrollment_required: true`. Middleware then allows only `/api/me`,
  `/api/logout`, `/api/mfa/enroll` and `/api/mfa/confirm`. Every other route
  returns `403`.
- This closes the window for a legacy account and for an `employee` who is
  promoted to `chef`. A role change also revokes the target's sessions.

### TOTP and recovery codes

- TOTP uses SHA-1, 6 digits and a 30-second step, with a skew of one step in
  each direction. These are the values every common authenticator app supports.
- The seed is 20 random bytes, encoded as base32 without padding.
- Recovery codes are 10 single-use codes in `XXXXX-XXXXX` form. They use an
  alphabet without easily confused characters. Only a SHA-256 hash is stored.
- Codes are shown once at enrollment or regeneration. Regeneration replaces the
  old set in the same transaction.
- Disabling MFA and regenerating recovery codes require the current password and
  a valid second factor.
- The challenge token is stored as a hash, lives 5 minutes, allows 5 attempts
  and is consumed in the same transaction as the session. A `:one` update
  prevents a replay race.

### Seed encryption

- The TOTP seed is encrypted with AES-256-GCM.
- The additional authenticated data is
  `v<keyVersion>|<tenantID>|<userID>|mfa_secret`. A ciphertext copied to another
  account or tenant fails to open.
- The key is `MFA_ENCRYPTION_KEY`. If that variable is unset, the key is derived
  from `PATIENT_ENCRYPTION_KEY` with HMAC-SHA-256 and the label
  `altserv-mfa-secret-v1`.
- Configuration rejects a direct reuse of the patient encryption key as the MFA
  key. Domain separation keeps the two uses apart.
- `MFA_KEY_VERSION` is stored with each ciphertext so a rotation can re-encrypt
  old rows.

### Password reset

- The public request endpoint always answers `202` with the same message. It
  never reveals whether an account exists.
- A reset token is random, stored as a hash, lives 30 minutes and is single use.
  Consumption uses a `:one` update.
- A confirmed reset changes the password, consumes the token, revokes every
  session for that user and writes an audit event in one transaction.
- A `chef` or `platform_admin` may issue a token for another user in the same
  tenant. The token is returned to the issuer because no email sender exists.
- A `chef` may not reset a `platform_admin`. No user may use the admin endpoint
  for their own account.
- The public reset endpoints have a separate limit of 5 attempts per minute per
  source address.
- In the `dev` platform only, the public request returns the token so the flow
  can be tested. Production does not return it.

### User disable

- A `chef` or `platform_admin` can disable and enable another user in the same
  tenant.
- Disabling revokes every session for that user in the same transaction.
- A disabled account cannot log in. `GetSessionUser` and the login lookup exclude
  disabled users, so an old session cannot survive.
- Guard rails:
  - no self-disable
  - a `chef` cannot disable a `platform_admin`
  - the last active `chef` cannot be disabled
- Enabling clears the flag but does not restore any old session.

### Session purge

- `purge_expired_auth_rows()` is a `SECURITY DEFINER` function. It deletes
  expired and revoked sessions, expired MFA challenges and expired reset tokens.
- It inserts one `auth.sessions_purged` audit event per affected tenant in the
  same transaction as the delete.
- The purge runs once at startup and then every `SESSION_PURGE_INTERVAL`
  (default one hour).
- `altserv_sessions_purged_total` counts removed rows. A failed run increments
  the existing job-error metric and is logged.

### Audit

The new actions are audited:

- `auth.mfa_enrollment_started`
- `auth.mfa_enabled`
- `auth.mfa_disabled`
- `auth.recovery_codes_regenerated`
- `auth.password_reset_requested`
- `auth.password_reset_issued`
- `auth.password_reset_completed`
- `auth.sessions_purged`
- `user.disabled`
- `user.enabled`

The metadata holds a role where needed. It never holds a password, a token, a
TOTP seed, a recovery code, an email address or health data.

## Reasons

- A role-based MFA rule is simple to test and easy to explain. Making every role
  use MFA would slow down employees who often work on a shared care device.
- Returning a challenge before the cookie means a stolen password alone cannot
  create a session.
- The enrollment gate is the safest migration path. It does not lock out an
  existing account and it does not silently allow one-factor access.
- TOTP needs no SMS provider, no email sender and no third party. It works
  offline.
- Recovery codes prevent a lost phone from becoming a permanent lockout.
- Encrypting the seed protects it if a database dump leaks. Binding the
  ciphertext to tenant and user stops a row swap.
- A hashed, single-use reset token is the minimum for a safe reset. A generic
  answer avoids account enumeration.
- A `SECURITY DEFINER` purge is the only way to delete rows across tenants
  without giving the application broad delete rights in a request path.
- Revoking sessions on disable and role change removes the window where an old
  identity keeps old rights.

## Consequences

- Every privileged account must enroll MFA before it can use the system. The
  operator must plan this for the first login after deploy.
- Recovery codes are the only fallback. The operator must tell users to store
  them safely. There is no support bypass.
- No email sender exists. An administrator must hand a reset token to the user
  out of band.
- The MFA key is now part of the secret set. It must be backed up and rotated
  like the patient key. A lost MFA key locks privileged users out until they
  re-enroll.
- The reset and challenge tables add rows. The purge bounds them.
- The rate limiter is still per process. A multi-instance deployment needs a
  shared store.
- The login response shape changed. A client must handle `202` and the
  `mfa_enrollment_required` flag.

## Risks

- A user who loses both the authenticator and the recovery codes cannot log in.
  The operator must have a documented identity-verification procedure.
- A leaked reset token is a full account takeover until it expires. The 30-minute
  lifetime and the rate limit reduce, but do not remove, this risk.
- A leaked MFA key exposes every TOTP seed in the database. Rotate the key and
  re-enroll.
- A clock skew larger than one step causes a valid code to fail. Clients should
  use automatic time sync.
- The in-memory rate limiter resets on restart and is not shared. It is not a
  defence against a distributed attack.
- The `dev` token exposure must never be enabled in production. The code checks
  the platform name, but a misconfigured environment is still a risk.

## Open questions

- Should MFA also be required for `employee` accounts that can read patient data?
- Which email or SMS provider will deliver reset and enrollment messages?
- How long should a reset token live for a user who is offline for a day?
- Should the purge interval be tied to the session lifetime instead of a fixed
  default?
- Should a user be able to see and revoke individual sessions?
- Should recovery codes expire, and how often must they be regenerated?
