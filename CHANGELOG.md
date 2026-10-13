# @pgbeam/terraform

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
