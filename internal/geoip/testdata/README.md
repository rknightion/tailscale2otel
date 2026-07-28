# Test databases

`GeoIP2-Country-Test.mmdb` and `GeoLite2-ASN-Test.mmdb` are MaxMind's own test databases,
taken verbatim from [`maxmind/MaxMind-DB`](https://github.com/maxmind/MaxMind-DB) under
`test-data/`. That repository is dual-licensed MIT / Apache-2.0, which permits redistribution;
the databases contain synthetic records, not a licensed GeoLite2 extract.

**No real GeoLite2 or GeoIP2 database is committed to this repository** — they are ~10-30 MB
each, rebuilt twice a week, and covered by the GeoLite End User License Agreement. Operators
supply their own (or let `enrichment.geoip.download` fetch them).

Useful records in these fixtures, for writing tests:

| address | country DB | ASN DB |
| --- | --- | --- |
| `81.2.69.142` | `country.iso_code=GB`, `continent.code=EU` (note `registered_country=US`) | — |
| `89.160.20.128` | `SE` / `EU` | — |
| `2001:218::1` | `JP` / `AS` | — |
| `2a02:d500::` | continent `EU`, **no country at all** | — |
| `1.0.0.1` | — | AS15169 "Google Inc." |
| `12.81.96.1` | — | AS7018, **no organization** |
| `4.4.4.4` | not present | not present |
