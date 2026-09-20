---
title: Terraform conventions
tags: [infra, terraform, iac]
---
# Terraform conventions

## Layout

One root module per environment and region (`envs/prod-eu`, `envs/prod-us`,
`envs/staging`) composing shared modules under `modules/`. Modules are
versioned by git tag and pinned in root modules.

## State

Remote state in the object store with locking; one state file per root
module. Nobody runs `apply` locally against production: changes go through
the pipeline, which posts the plan on the pull request and applies after
merge.

## Naming and tagging

Resources are named `<env>-<service>-<purpose>` and tagged with `service`,
`owner`, `env`, and `cost-center`. Untagged resources are reported daily.

## Secrets

Terraform never stores secret values in state: secrets are created by the
secrets manager and referenced by name.

## Drift

A nightly plan detects drift; unexplained drift opens a ticket for the
owning team.
