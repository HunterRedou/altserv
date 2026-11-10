# ADR-006: Session authentication

## Problem
How do browsers authenticate against the API without storing credentials or
long-lived bearer tokens in JavaScript-accessible storage?

## Alternatives
- JWT in `localStorage`
- JWT in an `HttpOnly` cookie
- Server-side sessions with an opaque cookie
- HTTP Basic authentication

## Decision
Server-side sessions. The client receives a random 32-byte token in a
`__Host-session` cookie. The database stores only the SHA-256 hash of the token
in `sessions.token_hash`.

Every session also stores a CSRF token. Unsafe methods must send it in the
`X-CSRF-Token` header; `middlewareCSRF` compares it in constant time.

## Reasons
- Revocation is immediate: delete or mark the session row.
- No claims or keys to rotate in the client.
- A database leak does not expose usable session tokens.
- The cookie flags (`Secure`, `HttpOnly`, `Path=/`, `SameSite=Lax`, `__Host-`
  prefix) follow doc 6 and the OWASP session guidance.
- The tenant always comes from the session, never from a request header or
  body.

## Consequences
- Every protected request costs one indexed session lookup.
- Expired and revoked sessions must be cleaned up periodically
  (`DeleteExpiredSessions`).
- Login must rotate the session token (a fresh token per login).
- Session duration is configurable (`SESSION_DURATION`, default 24h, max 30d).

## Risks
- A stolen cookie is usable until expiry or logout; TLS and the `Secure` flag
  are mandatory.
- CSRF protection depends on clients sending the header; browsers alone cannot
  perform the unsafe request.
- Login rate limiting and MFA for privileged accounts are still open
  (doc 6).
