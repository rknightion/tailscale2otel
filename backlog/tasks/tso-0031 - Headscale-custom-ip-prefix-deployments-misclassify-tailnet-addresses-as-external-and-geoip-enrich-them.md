---
id: TSO-0031
title: >-
  Headscale custom ip-prefix deployments misclassify tailnet addresses as
  external and geoip-enrich them
status: Done
assignee: []
created_date: '2026-08-30 08:45'
updated_date: '2026-08-30 12:58'
labels: []
milestone: m-1
dependencies: []
priority: medium
type: bug
ordinal: 34000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
enrich.IsTailscaleAddr hardcodes the Tailscale CGNAT/ULA ranges (internal/enrich/devicecache.go:566-572) and internal/geoip/geoip.go:619-651 repeats them. A Headscale deployment using a non-default ip-prefix therefore gets every tailnet address labelled external, and those private addresses are then geo/ASN-enriched as if public. The enrich package doc admits the classification limitation, but geolocating private tailnet addresses looks unintended either way. Suspected during a product-surface review (2026-08-30), unproven - verify with a fixture using a custom prefix. Likely fix: derive the tailnet-address bounds from a configurable prefix set (Headscale exposes its prefixes) shared by both packages instead of two hardcoded copies.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 With a custom Headscale ip-prefix configured, tailnet addresses are classified as tailnet, not external
- [x] #2 Private/tailnet addresses are never geoip-enriched
- [x] #3 The address-range logic has one source of truth shared by enrich and geoip
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check passes (the full gate; it is what CI enforces)
- [x] #2 just gen leaves no diff (only if a generated artifact's inputs changed)
- [x] #3 just --fmt --check passes and every new recipe has a # doc comment and a [group(...)]
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
## Frozen seam (do not renegotiate)

New config key, Headscale-scoped, list of CIDR strings:

```
headscale:
  ip_prefixes: []    # tailnet address ranges this Headscale allocates from; empty = the Tailscale defaults (100.64.0.0/10, fd7a:115c:a1e0::/48). Must be private/CGNAT/ULA space.
```

- Go: `IPPrefixes []string `yaml:"ip_prefixes"`` on `HeadscaleConfig` (internal/config/config.go:461).
- Default: `nil` in `internal/config/defaults.go`; empty means the two built-in prefixes, so behaviour is unchanged for every existing deployment.
- Env: a plain `[]string`, so it MUST be added to `listEnvKeys` in internal/config/env.go:24 (comma-separated `TS2OTEL_HEADSCALE__IP_PREFIXES`), exactly like `collectors.devices.attribute_namespaces`.
- Four config seams apply — see the goal file. `TestExampleConfigCoversEveryKey` and `TestHelmValuesCoverEveryKey` (internal/config/completeness_test.go:74 and below it) make config.example.yaml AND deploy/helm/tailscale2otel/values.yaml mandatory, not optional.

## Validation is the security control (SECURITY-class change)

`Validate()` rejects `headscale.ip_prefixes` when the provider is not `headscale`, and rejects any entry that:
- fails `netip.ParsePrefix`, or is not `Masked()`-canonical;
- contains a globally routable unicast address — i.e. accept ONLY prefixes fully inside RFC1918, `fc00::/7`, or `100.64.0.0/10`;
- is loopback, link-local, multicast, unspecified, or `0.0.0.0/0` / `::/0`;
- is wider than /8 (v4) or /16 (v6), so a typo cannot swallow the address space.

Write the test for each rejection branch FIRST and watch it fail. This is the acceptance evidence for AC#2: the property "private/tailnet addresses are never geoip-enriched" is preserved BY CONSTRUCTION, because a prefix that could be geolocated cannot be configured.

## One source of truth

1. New leaf type in `internal/enrich` (no new package — geoip already imports nothing from enrich, so put the type where `IsTailscaleAddr` lives and have geoip depend on it, or extract a tiny `internal/tsnet`-style leaf if that creates a cycle; check the import direction before choosing).
2. `IsTailscaleAddr` keeps its current signature and behaviour as the DEFAULT set. Add a set-carrying form (e.g. `type AddrSet []netip.Prefix` with `Contains(netip.Addr) bool` and a `DefaultAddrSet`) and thread the configured set from the composition root into `DeviceCache` and into `nodediscovery.pickAddress`.
3. `geoip.Enrichable` keeps `IsPrivate()` and its named guards. Replace only the two duplicated literal prefixes with the shared default set so the names stay greppable. Do NOT make `Enrichable` configurable — a config value must never be able to turn geolocation ON for an address it currently refuses.

## Tests (all test-first)

- `IsTailscaleAddr`/`AddrSet` with a custom prefix classifies `10.100.0.5` as tailnet, and still classifies `8.8.8.8` as external.
- `DeviceCache.resolve` returns `unknown`/`ProvenanceNone` (not `external`) for an uncached address inside a configured custom prefix.
- `pickAddress` accepts an address in a configured custom prefix and STILL rejects `169.254.169.254`, `127.0.0.1`, `::1`, a link-local, and a public address — the SSRF regression cases, negative-tested.
- `Enrichable` is unchanged for every address in the probe table above.

## Wiring and regeneration

`internal/app` composition root passes the resolved set to the device cache and the node-metrics discovery. That is a wiring-pass file (doc-0002 single-owner list) — the lane must not edit it concurrently with another lane.

Regenerate in the same commit: `just gen-config-schema`, `just gen-envref`, `just gen-helm`. Gate: `just check`.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
## Research 2026-08-30 (Wave 1 planning, HEAD 1dd76a9) — one AC confirmed, one REFUTED, and a security seam the task did not mention

Probed both packages empirically (throwaway tests, since removed):

```
IsTailscaleAddr(10.100.0.5)        = false      <- a plausible custom Headscale prefix
IsTailscaleAddr(fd00:dead:beef::1) = false
IsTailscaleAddr(100.64.1.2)        = true
IsTailscaleAddr(fd7a:115c:a1e0::1) = true

Enrichable(10.100.0.5)        = false  (IsPrivate=true)
Enrichable(172.20.5.1)        = false  (IsPrivate=true)
Enrichable(192.168.44.9)      = false  (IsPrivate=true)
Enrichable(fd00:dead:beef::1) = false  (IsPrivate=true)
Enrichable(fdaa:bbbb::5)      = false  (IsPrivate=true)
Enrichable(100.64.1.2)        = false  (CGNAT guard)
Enrichable(fd7a:115c:a1e0::1) = false  (ULA guard)
Enrichable(8.8.8.8)           = true
Enrichable(2606:4700::1111)   = true
```

**AC#1 CONFIRMED.** `enrich.IsTailscaleAddr` (internal/enrich/devicecache.go:570) hardcodes only `100.64.0.0/10` and `fd7a:115c:a1e0::/48`, so on a Headscale deployment with a custom `prefixes` setting every tailnet address falls through `DeviceCache.resolve` (devicecache.go:505) to the literal string `"external"` with `ProvenanceNone`. Flow-log peer naming is wrong for the whole tailnet.

**AC#2 REFUTED AS WRITTEN.** The task says those private addresses "are then geo/ASN-enriched as if public". They are not. `geoip.Enrichable` (internal/geoip/geoip.go:629) rejects on `addr.IsPrivate()` BEFORE reaching the named Tailscale guards, and Gos `netip.Addr.IsPrivate` covers RFC1918 and all of `fc00::/7`. Every private custom prefix a Headscale operator could realistically choose is already excluded. Only a Headscale deployment addressing its tailnet out of GLOBALLY ROUTABLE space would leak, which is a pathological configuration. So there is no geoip bug to fix here; the requirement is to keep the property true, by validating that a configured prefix is non-global.

**AC#3 CONFIRMED but narrower than stated.** `geoip.tailscaleULA` (geoip.go:619) and `geoip.cgnat` (geoip.go:651) do duplicate `enrich.tsULA`/`tsCGNAT` — but geoips copies are a documented belt-and-braces guard on top of `IsPrivate`, and geoips comment says so explicitly. Sharing one source of truth is worth doing; deleting geoips guards is not.

## The seam the task missed, and it is the risky one

`enrich.IsTailscaleAddr` has a SECOND caller and it is a security control, not a labelling helper: `pickAddress` in internal/app/nodediscovery.go:293. Its own comment (nodediscovery.go:280-297) states that restricting scrape targets to the Tailscale ranges is what stops a compromised or buggy control plane turning the node-metrics scraper into an SSRF client against cloud metadata endpoints, loopback admin ports and RFC1918 services. This came out of the 2026-06-09 security review.

Making the prefix set operator-configurable therefore WIDENS an SSRF guard. That must be a deliberate, validated widening, not a side effect of a labelling fix.

## Headscale does not expose its prefixes — the task description is wrong on this

The description proposes deriving the bounds from the control plane because "Headscale exposes its prefixes". It does not. `internal/hsapi/client.go` calls exactly five endpoints — `/api/v1/node`, `/user`, `/preauthkey`, `/apikey`, `/policy` (client.go:238-278, limit.go:20-21) — and Headscales v1 API has no configuration/prefixes resource. `hsapi.APIKey.Prefix` (types.go:68) is an API-key string prefix, unrelated. The prefix set must come from CONFIG.

Wave 1 Lane A3 started by root at 268fc93 after Config Freeze f54548a; AC#2 is treated as refuted-as-written per goal §6.2 and W1 wiring remains root-owned.

Validation: custom-prefix containment and cache classification passed; deliberate breaks of address-set containment, private-address rejection, and global-address handling failed the named focused assertions and were restored. Root wiring and SSRF rejection tests passed in just check; exact-head CI run 33312668201 concluded success.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Landed 1fbb579 with root wiring in 1de673f: validated configured Headscale private prefixes, shared address classification, and preserved node-discovery SSRF rejections. Verified by race tests, full gate, and CI 33312668201.
<!-- SECTION:FINAL_SUMMARY:END -->
