# ADR-014: Versioned patient-specific templates

## Problem
Nurses repeat the same set of activities for a patient (morning care, evening
care, weekend care). Re-typing them is slow and error-prone. At the same time a
template must never silently become a performed, billable activity, and a
historical report must still show which template version was used even after the
template has been edited or retired (doc 11).

## Alternatives
- Mutable templates that overwrite their activity list
- Templates that copy themselves into performed activities on load
- Free-text templates without references to the service catalog
- Immutable template versions that reference catalog service definitions

## Decision
A template is a patient-specific, versioned suggestion list:

- `templates` stores `tenant_id`, `patient_id`, `name`, `created_by`, `version`
  and `status` (`active`, `superseded`, `archived`).
- `template_activities` stores `template_id`, `activity_id`,
  `default_quantity` and `sort_order`. `activity_id` references a global
  `service_definitions` row (the catalog activity), never a performed
  `activities` row.
- A unique partial index allows only one `active` version per
  `(tenant_id, patient_id, name)`. Multiple templates per patient are normal
  (morning, evening, weekend).
- Editing an active template supersedes it and inserts a new version with
  `version + 1`. Versions are immutable, so a report can cite the exact
  template id and version it used and old reports never change.
- Only the active version can be edited or archived. Creating a template whose
  name already has an active version returns a conflict.
- Archiving retires the active version; historical versions remain readable.

Loading a template is a suggestion step for the report UI. It never creates
`activities` or report rows by itself; a human still confirms what was done.

Access rules:

- `chef` may manage templates for any patient in the tenant.
- `employee` may create, edit and read templates only for patients they are
  currently assigned to. The check reuses
  `assignments.Service.CanAccessPatient`, so revocation takes effect
  immediately.
- `platform_admin` gets no automatic template or patient access (ADR-004 and the
  platform-admin reminder).
- An unassigned employee receives `404` for reads so the template's existence
  is not leaked, and `403` for writes.

## Reasons
- Immutable versions keep historical reports stable without extra copy tables.
- Referencing service definitions keeps SGB XI / SGB V billability explicit and
  prevents free-text activities from becoming billable by accident.
- One shared assignment check keeps patient access consistent across patients,
  activities and templates.
- A partial unique index enforces "one active version per name" in the database
  instead of relying only on application logic.

## Consequences
- Every edit grows the table by one template row plus its activities; old
  versions are never deleted.
- Reports (doc 13) must store the template id and version they were created
  from. That column is added with the report schema, not here.
- The API exposes both `/api/patients/{id}/templates` and
  `/api/templates/{id}` so a report can fetch a specific historical version.
- Template activities are ordered by `sort_order`, then creation time.

## Risks
- A long-lived patient can accumulate many superseded versions. Reads only
  return active versions by default, so the UI stays small, but storage grows.
- If a catalog service definition is removed, `ON DELETE RESTRICT` blocks the
  deletion while templates reference it. Retiring a definition must be done by
  ending its validity window, not by deleting the row.
- Two concurrent edits to the same active version: the supersede update is
  scoped to `status = 'active'`, and the unique partial index prevents two
  active versions, so the second writer fails instead of forking history.

## Open questions
- Should archived templates be restorable, and would that reuse the old version
  number or create a new one?
- Do reports need to freeze the template activity labels as text, in addition to
  the template id and version?
- Should there be tenant-wide template presets that are copied to a patient
  instead of per-patient templates only?
