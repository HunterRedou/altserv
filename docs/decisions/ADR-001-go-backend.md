# ADR-001: Go backend

## Problem
Choose the language and runtime for the backend.

## Alternatives
- Node.js / TypeScript
- Python / Django
- Java / Spring Boot
- Go with `net/http`

## Decision
Go with the standard library `net/http` server.

## Reasons
- One static binary, simple deployment.
- Strong standard library for HTTP, TLS, crypto and context.
- Static typing catches mistakes at compile time.
- Goroutines fit background jobs such as tenant verification.
- Matches the learning resources in doc 24 (Let's Go, Let's Go Further).

## Consequences
- Routing, middleware and error responses are written by hand at first.
- The team must learn Go idioms.

## Risks
- More boilerplate than a full framework.
- Without discipline, handler logic can leak into the HTTP layer.
