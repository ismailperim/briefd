---
title: Networking and service exposure
tags: [infra, networking, security]
---
# Networking and service exposure

## Public surface

Only the API gateway, the merchant portal, and the webhook egress are exposed
publicly. Everything else is reachable only inside the cluster network. The
gateway terminates TLS, enforces rate limits per API key (default 100
requests per second), and forwards a `request_id`.

## Service-to-service

Services call each other through the mesh with mutual TLS; the JWT from
`identity` carries the caller's service name. Network policies deny all
traffic by default and allow only declared dependencies.

## Egress

Outbound calls to acquirers and banks go through a fixed egress IP set that
partners allow-list. Adding an egress destination is a change request
reviewed by security.

## IP allow-lists for merchants

Merchants can restrict API keys to CIDR ranges; requests from other addresses
receive `ip_not_allowed` and are logged for the merchant.

## Timeouts at the edge

The gateway times out requests after 30 seconds; services should fail faster
than that so the client gets a structured error instead of a gateway timeout.
