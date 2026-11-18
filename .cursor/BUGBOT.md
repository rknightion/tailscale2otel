# Tailscale exporter review

## Operator scope

This exporter turns Tailscale API and log signals into OpenTelemetry and Prometheus surfaces. Review configuration, telemetry and admin API changes against existing operators and documented compatibility.

## Signal contracts

Keep one ingestion path for each log type. Check the telemetry catalog, Prometheus names and unit suffixes against downstream queries. Review label cardinality and source and sink behavior as well as the dashboard and alert coverage of changed signals.

## Configuration and deployment

Review config changes across the schema, environment overrides and chart values. Map and structured-list inputs need consistent treatment across those surfaces. Preserve the Helm and admin endpoint contracts when wiring changes alter a release artifact.

## Sensitive paths

Check tailnet authentication, retry, pagination and polling changes against authorized capture or synthetic request fixtures as appropriate. Keep credentials, device or account identifiers and real observability captures out of public evidence.

## Verification

Run `just check` with the pinned tools. Regenerate full outputs at their source and review the handwritten contract and generator changes. Use the repository's native module and Helm checks for the relevant behavior.
