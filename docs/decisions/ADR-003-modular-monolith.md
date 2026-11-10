# ADR-003: Modular monolith

## Problem
Choose the deployment architecture for the first version.

## Alternatives
- Microservices
- Serverless functions
- Modular monolith

## Decision
A modular monolith: one Go binary with clear domain modules.

## Reasons
- Small team and one deployable unit.
- Transactions across domains stay simple.
- No Kubernetes, Kafka or service mesh (doc 25).
- Module boundaries can be extracted later if needed.

## Consequences
- Packages must be separated by domain (`internal/patients`, `internal/server`, ...).
- Cross-module calls go through explicit interfaces, not shared globals.

## Risks
- Boundaries erode if everything imports everything.
- A single process failure affects all modules.
