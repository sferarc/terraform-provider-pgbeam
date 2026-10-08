# PgBeam Terraform Provider

Terraform provider for [PgBeam](https://pgbeam.com): manage your globally distributed PostgreSQL proxy infrastructure as code.

## Usage

The provider is not in the Terraform Registry yet, so `terraform init` cannot resolve `sferarc/pgbeam` from it. Until it is, download a build from [GitHub releases](https://github.com/sferarc/terraform-provider-pgbeam/releases) and point Terraform at it with a [`dev_overrides`](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers) block in your Terraform CLI configuration. The `source` below is the address it will have once published.

```hcl
terraform {
  required_providers {
    pgbeam = {
      source = "sferarc/pgbeam"
    }
  }
}

provider "pgbeam" {
  api_key = var.pgbeam_api_key
}

resource "pgbeam_project" "main" {
  name   = "my-project"
  org_id = var.org_id
}

resource "pgbeam_database" "primary" {
  project_id = pgbeam_project.main.id
  host       = var.db_host
  port       = 5432
  name       = "mydb"
  username   = var.db_user
  password   = var.db_password
  ssl_mode   = "require"
}
```

A project does not take a `region`: by default (`residency = "any"`) PgBeam serves it from every metro and routes each client to the nearest one. Set `residency` to `"us"` or `"eu"` to require the serving metro to be in that jurisdiction. Where a connection pool lives is a per-database choice, via `pool_region`. On `pgbeam_database`, `name` is the PostgreSQL database name on your server.

## Resources

| Resource | Description |
| --- | --- |
| `pgbeam_project` | PgBeam project |
| `pgbeam_database` | PostgreSQL database connection |
| `pgbeam_replica` | Read replica configuration |
| `pgbeam_custom_domain` | Custom domain for connection strings |
| `pgbeam_cache_rule` | Query caching rule |
| `pgbeam_spend_limit` | Budget controls |
| `pgbeam_agent_credential` | Scoped agent credential (one-time secrets) |
| `pgbeam_webhook_endpoint` | Event delivery endpoint |
| `pgbeam_policy_profile` | Policy profile (access mode, allowlists, masking, budgets) |

## Agent gateway

The agent gateway issues scoped, policy-enforced database credentials for AI agents and delivers audit/anomaly events to webhook endpoints.

```hcl
resource "pgbeam_webhook_endpoint" "audit" {
  project_id  = pgbeam_project.main.id
  url         = "https://example.com/hooks/pgbeam"
  format      = "json"
  event_types = ["blocked", "anomaly", "approval"]
  secret      = var.webhook_secret # write-only; never read back
  enabled     = true
}

resource "pgbeam_agent_credential" "analytics" {
  project_id        = pgbeam_project.main.id
  policy_profile_id = var.policy_profile_id
  name              = "Claude Code (analytics)"
  principal_type    = "agent"
}

# One-time secrets returned only at creation. They are stored in state, marked
# sensitive, and cannot be retrieved again — rotate by replacing the resource.
output "agent_connection_string" {
  value     = pgbeam_agent_credential.analytics.connection_string
  sensitive = true
}

output "agent_mcp_url" {
  value = pgbeam_agent_credential.analytics.mcp_url
}

output "agent_mcp_token" {
  value     = pgbeam_agent_credential.analytics.mcp_token
  sensitive = true
}
```

> **Agent credential secrets caveat.** `connection_string` and `mcp_token` are generated once at creation and are never returned by later reads (`mcp_url` is not secret). They are persisted in Terraform state as sensitive computed outputs. To rotate, taint or replace the resource (`terraform apply -replace=pgbeam_agent_credential.analytics`).

Manage policies as code with the `pgbeam_policy_profile` resource, then reference its ID wherever a profile is required:

```hcl
resource "pgbeam_policy_profile" "read_only" {
  project_id  = pgbeam_project.main.id
  name        = "read-only"
  access_mode = "read_only"
}
```

Reference `pgbeam_policy_profile.read_only.id` wherever a profile ID is required: `policy_profile_id` on `pgbeam_agent_credential` (see the Agent gateway example above), or `default_policy_profile_id` on `pgbeam_project` to enforce a profile on passthrough/human connections. Keeping the profile in Terraform puts the most security-sensitive primitive under `terraform plan` drift detection.

## Authentication

Set the `PGBEAM_API_KEY` environment variable or configure the `api_key` argument in the provider block.

## Documentation

Full usage guide at [pgbeam.com/docs/terraform](https://pgbeam.com/docs/terraform).

## Development

```bash
go build -o terraform-provider-pgbeam
```

## Contributing

Issues and pull requests are welcome here. An issue is the right place to start for a bug, a wrong doc, or a missing capability; say what you ran, what happened, what you expected, and which version you were on.

To build and test it locally:

```bash
go build ./...
go test ./...
```

Do not open a public issue for a suspected security vulnerability. Email security@pgbeam.com, or report it privately from this repository's Security tab.

## License

Apache 2.0 — see [LICENSE](LICENSE).
