# Owner decisions

Only the owner can make these decisions. Write the answer, the date and the
reason under each question. One answer is enough to unblock the next work
package.

## D-1: Hosting

Unblocks: WP-07, WP-13, WP-17.

1. Do you have an AWS account?
2. Do you want Terraform (reproducible, reviewable) or a console/CLI runbook
   first?
3. Where does the Go app run: EC2, ECS Fargate or App Runner?
4. Multi-AZ from the start, or single-AZ to keep the cost low?

Context:

- RDS for PostgreSQL in eu-central-1 supports RLS, `SET LOCAL ROLE`,
  `SECURITY DEFINER` and the audit triggers. The migrations need no extension.
- PITR is built in: 1 to 35 days, about 5 minutes RPO. A restore always makes
  a new instance.
- Set at create time: KMS encryption, private subnet, `rds.force_ssl=1`,
  deletion protection, backup retention. Optional: AWS Backup Vault Lock.
- ECS Fargate can inject Secrets Manager values as environment variables. Then
  the app code does not change.
- Check § 393 SGB V (C5 attestation, EU processing) with E-1.
- Check if the AWS European Sovereign Cloud is available and what it costs.

Answer:

## D-2: RPO and RTO

Unblocks: WP-10.

1. How much data can the customer lose (RPO)?
2. How fast must the service be back (RTO)?

Context: RDS PITR gives about 5 minutes RPO. Only a drill measures the RTO.

Answer:

## D-3: Activation and payment

Unblocks: WP-15, E-5.

1. Is the payment for tenants who pay for altserv (subscription)? The plan
   assumes yes. It is not related to care-fund billing.
2. PayPal or Stripe? Many German B2B customers prefer SEPA direct debit.
3. After VIES is removed: keep the USt-IdNr as an optional field for invoices,
   or remove it?

Context:

- Today VIES is the activation gate: `pending` to `valid`. An invalid tenant is
  deleted. Login needs `valid`.
- Many care services are VAT-exempt (§ 4 Nr. 16 UStG) and can have no
  USt-IdNr. E-5 must confirm this.
- A failed payment must suspend a tenant. It must never delete care data.
- WP-05 adds manual activation. The pilot can start with manual activation.

Answer:

## D-4: GDPR roles

Unblocks: E-1, E-4.

1. Is the care service the controller and altserv the processor?
2. Who signs the Art. 28 agreement for altserv?

Answer:

## D-5: Pilot UI

Unblocks: WP-20.

1. Which screens does the pilot need?
2. Which technology: server-rendered HTML, or a JavaScript framework?

Context: today only `start.html` exists. Care staff cannot work with a bare
API.

Answer:

## D-6: Pilot scope

Unblocks: WP-20.

1. Which features use real data in the pilot?
2. Which features stay off (for example billing export)?

Answer:

## D-7: Support model

Unblocks: WP-18.

1. How does a `platform_admin` help a tenant?
2. Is a tenant-wide break-glass grant necessary, or is one grant for each
   patient enough for the pilot?

Answer:
