# @pgbeam/terraform

## 0.3.0

### Minor Changes

- 796f4a7: Policy profiles carry `query_mode` (`sql` or `templates`). A profile set to `templates` admits only the project's enabled query templates, and every other rule on the profile still applies to what a template runs. Setting `templates` is refused when the project has no enabled template, and while the organization has a live self-hosted enrollment. On update, an omitted `query_mode` keeps the stored mode. In Terraform, Crossplane and Pulumi, removing `query_mode` from a config plans `sql`, and the plan shows that change.

## 0.2.0

### Minor Changes

- d651ca6: Query templates can be declared as code: `pgbeam_query_template`, the Crossplane `QueryTemplate` and the Pulumi `QueryTemplate`. Removing `description`, `include_sql`, `enabled` or `params` from a config resets it to the API default (empty, false, true, none) rather than keeping the last value. The query template input's `description` now declares its default (empty) in the contract, which is what the API already stored for an omitted one. Nothing restricts a credential to templates yet: the policy profile setting that does arrives in a later release.

### Patch Changes

- 399356e: `terraform import` now rejects composite IDs with an empty, blank or extra segment, such as `prj_1/` or `prj_1/db_1/extra`, and names the expected format. Previously these were sent to the API, where an extra segment addressed a different route.

## 0.1.7

### Patch Changes

- 223cb7c: fix(deps): clear govulncheck GO-2026-6809 (crypto/x509) with Go 1.26.10
- 985ea4c: Reject a `base_url` that would send the API key over plaintext HTTP or that has no scheme or host, and trim whitespace from the API key so a key read from a file with a trailing newline works.
- 62933e2: Importing a `pgbeam_cache_rule` now records `project_id` and `database_id` from the import ID. Previously both were left empty, so the next refresh listed the wrong path and dropped the imported rule from state.

  Refreshing `pgbeam_replica`, `pgbeam_custom_domain`, `pgbeam_cache_rule` or `pgbeam_self_host_enrollment` now fails with an error if the API returns the same page token twice, instead of paging forever.

- deb0ad2: An in-place update of any resource that exposes `updated_at` no longer fails with "Provider produced inconsistent result after apply". The plan used to carry the old `updated_at` as a known value while the API set a new one, so Terraform rejected the apply after the change had already been made. `updated_at` now shows as "(known after apply)" when the resource changes, and a plan with no changes is unaffected.

## 0.1.6

### Patch Changes

- ef1df01: fix(deps): clear govulncheck with Go 1.26.9 and golang.org/x/net 0.60.0

## 0.1.5

### Patch Changes

- c1888e5: Release the AnomalyRule and ApprovalRule resources, which were added without a provider version bump.

## 0.1.4

### Patch Changes

- 4b54b03: A `pool_config` without `max_active` now plans the API's default of 200 instead of sending 0, and an `allowed_cidrs` entry without a `label` no longer fails apply with an inconsistent result.

## 0.1.3

### Patch Changes

- 3342c65: feat(policy): content_scan_mode and content_scan_max_bytes on the policy profile

## 0.1.2

### Patch Changes

- c645088: fix(providers): the published Terraform provider pinned a stale Go SDK because its go.mod did not carry the sentinel the release rewrites

## 0.1.1

### Patch Changes

- 5d49e15: feat(providers): SelfHostEnrollment resource in all three IaC providers, gateway-resource secret-lifecycle tests, crossplane generator fix for immutable resources with wrapped create responses
