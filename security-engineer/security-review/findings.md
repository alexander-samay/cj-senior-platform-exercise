# Security Findings Bundle

This bundle combines outputs from a cloud posture scanner, SAST, and a recent manual pentest. Treat scanner severity as an input, not the final answer.

Service under review: `forecasting-api`, a Python/FastAPI service deployed on AWS/EKS. The service exposes forecast search, forecast rerun, and import-status APIs. It uses Postgres for forecast metadata, S3 for report artifacts, and AWS IAM roles for AWS access.

Your job is to triage these findings, identify what is actually exploitable, fix the highest-risk issue you have enough context to fix, and explain what should happen next.

---

## F-001 — Dynamic SQL query uses untrusted request input

**Source:** Veracode SAST
**Scanner severity:** High
**Category / CWE:** Injection / CWE-89
**Component:** `app/repositories/report_repository.py`, `app/api/reports.py`
**Affected endpoint:** `GET /api/v1/reports/search`

**Description:**
The report-search path builds a raw SQL query using request-controlled values. The `q` query parameter is concatenated into a `LIKE` clause before being passed to the database through SQLAlchemy `text()`. The same query runs under the application database role used by the forecasting API.

**Scanner evidence:**
Untrusted HTTP query parameter reaches SQL construction without parameter binding. The scanner observed string interpolation in the repository layer and did not detect a prepared statement or bound parameter for the search term.

**Initial scanner recommendation:**
Ensure request-controlled input cannot alter the structure of the executed query. Add a regression test covering the search term.

---

## F-002 — ALB security group allows ingress from `0.0.0.0/0`

**Source:** Wiz cloud posture scan
**Scanner severity:** Critical
**Category / CWE:** Network exposure / CWE-284
**Component:** `infra/network.tf`
**Affected resource:** `aws_security_group.forecasting_api_alb`

**Description:**
The ALB security group allows TCP/443 ingress from `0.0.0.0/0`. The scanner classified this as public internet exposure for the forecasting API listener.

**Scanner evidence:**
Security group rule permits inbound HTTPS from all IPv4 sources. The attached load balancer is named `forecasting-api-internal`.

**Initial scanner recommendation:**
Restrict ingress to approved corporate/VPN CIDR blocks or to a known upstream security group.

---

## F-003 — Forecast rerun endpoint does not enforce forecast ownership

**Source:** Manual pentest
**Scanner severity:** High
**Category / CWE:** Broken access control / CWE-639, CWE-862
**Component:** `app/api/forecasts.py`, `app/services/forecast_service.py`
**Affected endpoint:** `POST /api/v1/forecasts/{forecast_id}/rerun`

**Description:**
The rerun endpoint checks that the caller is authenticated and has the `forecast:write` scope, then loads the requested forecast by `forecast_id`. It does not consistently verify that the forecast belongs to the caller's company or permitted company set before enqueueing the rerun job.

**Pentest evidence:**
A valid user token from one company was accepted for a rerun request targeting a forecast record owned by another company. The response did not return the full forecast result directly, but it did enqueue work using the target forecast's saved parameters.

**Initial pentest recommendation:**
Enforce company/tenant ownership before rerun. Add tests for same-company allowed, cross-company denied, and privileged support/admin behavior if applicable.

---

## F-005 — Deployable config contains Kubernetes Secret data in Git

**Source:** SAST / repository secret scan
**Scanner severity:** High
**Category / CWE:** Hardcoded credential / CWE-798
**Component:** `infra/secrets-config.yaml`
**Affected object:** `kind: Secret`, `metadata.name: forecasting-api-config`

**Description:**
A deployable Kubernetes Secret manifest contains `stringData` entries for database connection settings and an outbound analytics token. The values appear to be non-production test values, but the same manifest pattern is used by environment overlays.

**Scanner evidence:**
The repository contains secret-shaped values in a Kubernetes Secret manifest. The file is part of the deployable infrastructure path rather than documentation-only sample code.

**Initial scanner recommendation:**
Remove secret values from Git. Use an external secret reference or runtime secret provider. Rotate any value that may have been valid in a shared environment.

---

## F-007 — Possible SQL injection in report sort field

**Source:** SAST
**Scanner severity:** Medium
**Category / CWE:** Injection / CWE-89
**Component:** `app/repositories/report_repository.py`
**Affected endpoint:** `GET /api/v1/reports/search`

**Description:**
The SAST tool separately flagged the `sort` query parameter used in the report-search SQL `ORDER BY` clause. This appears related to F-001.

**Scanner evidence:**
Request-controlled `sort` value reaches raw SQL construction. The scanner could not prove that the value is restricted to a known list of sortable columns.

**Initial scanner recommendation:**
Consolidate with F-001 if the same patch fixes both.

---

## F-008 — Legacy import bucket reported as publicly readable; owner unclear

**Source:** Wiz cloud posture scan
**Scanner severity:** High
**Category / CWE:** Public cloud storage exposure / CWE-200, CWE-668
**Component:** Not found in this repo
**Affected resource:** `s3://forecasting-imports-legacy-lab`

**Description:**
A legacy S3 bucket associated with historical import testing is reported as publicly readable. The bucket is not declared in the provided Terraform files, and ownership tags are incomplete.

**Scanner evidence:**
The scanner reports public-read ACL or equivalent public access policy on the bucket. The bucket name suggests lab usage, but the finding does not include object inventory or data classification.

**Initial scanner recommendation:**
Identify owner and data classification. If still needed, disable public access and move ownership into IaC. If no longer needed, delete after confirming retention requirements.
