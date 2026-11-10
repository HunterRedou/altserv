# ADR-015: Visits with planned and actual deployment

## Problem
A care visit is not a report and not a performed activity. It is the planned or
actual deployment of one employee to one patient (doc 12). The system must keep
apart who was planned, who actually showed up, and when the visit really started
and ended, because substitutions and corrections happen in daily care. Reports
and later billing need to attach to a visit without the visit becoming the
report itself. Corrections after the fact must stay traceable.

## Alternatives
- One row that only stores the planned employee and overwrites it on substitution
- A visit that directly contains the report and its performed activities
- Free-form time strings instead of typed timestamps
- Separate planned and actual visit tables joined by an id
- A single visit row with distinct planned and actual fields

## Decision
A visit is one row in `visits` with two clearly separated sides:

- Planned side: `employee_id` and `planned_start`.
- Actual side: `actual_employee_id`, `actual_start` and `actual_end`.
- `status` is one of `planned`, `in_progress`, `completed`, `cancelled`.
- `patient_id`, `employee_id`, `actual_employee_id` and `created_by` use
  composite `(id, tenant_id)` foreign keys so a visit can never point at another
  tenant's patient or user.

Status transitions:

- `planned` → `in_progress` when an assigned employee (or the chef) starts it.
  The starting user is written to `actual_employee_id`. If that user differs
  from the planned employee, the visit and the audit log record a substitution.
- `in_progress` → `completed` by the actual employee or the chef. The database
  clock sets `actual_end`.
- `planned` or `in_progress` → `cancelled` by the chef.
- A completed or cancelled visit is terminal.

Only a chef plans, reschedules, cancels or corrects a visit. An employee may
start and complete a visit for a patient they are currently assigned to; the
assignment is checked against `clock_timestamp()` so a revocation takes effect
immediately. An unassigned employee receives `404` for reads and `403` for
writes. `platform_admin` gets no automatic access (ADR-004 and the platform-admin
reminder).

Corrections:

- A chef may correct `actual_start` and `actual_end` of a started visit.
- A non-empty reason is required and is stored in the `visit.corrected` audit
  event together with the previous and new times.
- An in-progress visit may only correct its start, never set an end; completing
  is the operation that ends a visit.
- End before start and times in the future are rejected.

Time storage:

- Visit time columns are `TIMESTAMPTZ`. Planned and actual times are instants,
  not wall-clock text. Storing them as `TIMESTAMP` shifted them by the database
  session offset and made a just-started visit look like it was in the future
  when corrected. `TIMESTAMPTZ` round-trips the instant regardless of session
  timezone.

## Reasons
- Two columns instead of two tables keeps the substitution explicit and makes
  "who was planned vs. who performed" a single read.
- The actual employee is recorded at start, not at planning, because that is the
  moment the substitution becomes real.
- Composite tenant foreign keys enforce tenant isolation in the database, in
  addition to RLS.
- A required correction reason makes every change after the fact explainable
  without a separate correction table.
- `TIMESTAMPTZ` removes a whole class of timezone bugs from corrections and
  reporting.

## Consequences
- Reports (doc 13) attach to a visit id; the visit does not embed the report.
- Billing can use `actual_start`, `actual_end` and `actual_employee_id`, and can
  see substitutions from the planned `employee_id`.
- A correction changes the visit row in place. The history of corrections lives
  in `audit_events`, not in the visit row, so reports should read the audit log
  when a full correction trail is needed.
- Cancelled visits stay in the table and remain visible to the chef.

## Risks
- In-place correction means the visit row no longer shows its previous times.
  The audit event is the only record, so audit retention becomes important.
- If the assignment is revoked between start and complete, the employee can no
  longer complete their own in-progress visit; the chef must complete it. This
  is deliberate, but it should be surfaced in the UI.
- Starting a visit by a non-planned assigned employee is allowed. If the
  business wants substitutions to require prior approval, a separate approval
  step would be needed.
- `clock_timestamp()` is used for actual times, so tests must not assume a fixed
  clock; they compare instants instead.

## Open questions
- Should a chef be able to reassign an in-progress visit's actual employee, or is
  that only possible by cancelling and re-planning?
- Do reports need to freeze the visit's planned and actual times as text, in
  addition to the visit id?
- Should there be a maximum visit duration or overlap warning when planning two
  visits for the same employee?
