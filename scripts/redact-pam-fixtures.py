#!/usr/bin/env python3
"""Build deterministic, public-safe PAM API fixtures from local captures."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


FIXTURE_NAMES = (
    "pam_organization.json",
    "pam_serverinfo.json",
    "pam_connectors.json",
    "pam_connector_one.json",
    "pam_connector_tokens.json",
    "pam_connector_plugins.json",
    "pam_sockets.json",
    "pam_socket_one.json",
    "pam_socket_connectors.json",
    "pam_socket_upstream_config.json",
    "pam_socket_builtin_ssh.json",
    "pam_sessions.json",
    "pam_socket_sessions.json",
    "pam_socket_sessions_empty.json",
    "pam_policies.json",
    "pam_policy_one.json",
    "pam_iam_users.json",
    "pam_iam_groups.json",
    "pam_iam_service_accounts.json",
)


class Redactor:
    def __init__(self) -> None:
        self._maps: dict[str, dict[str, str | int | float]] = {}

    def replacement(self, kind: str, value: Any) -> str | int | float:
        values = self._maps.setdefault(kind, {})
        key = json.dumps(value, sort_keys=True)
        if key in values:
            return values[key]
        index = len(values) + 1
        if kind == "id":
            replacement: str | int | float = f"00000000-0000-4000-8000-{index:012d}"
        elif kind == "email":
            replacement = f"person-{index}@example.invalid"
        elif kind == "ip":
            if index > 255:
                raise SystemExit("IPv4 replacement capacity exhausted")
            replacement = f"192.0.2.{index}"
        elif kind == "ipv6":
            if index > 0xFFFF:
                raise SystemExit("IPv6 replacement capacity exhausted")
            replacement = f"2001:db8::{index}"
        elif kind == "port":
            replacement = 50000 + index
        elif kind == "url":
            replacement = f"https://example.invalid/image-{index}.png"
        elif kind == "hostname":
            replacement = f"host-{index}.example.invalid"
        elif kind == "latitude":
            replacement = float(index)
        elif kind == "longitude":
            replacement = float(-index)
        else:
            replacement = f"fixture-{kind}-{index}"
        values[key] = replacement
        return replacement

    def redact(self, value: Any, key: str = "", parents: tuple[str, ...] = ()) -> Any:
        if value is None or isinstance(value, bool):
            return value
        if isinstance(value, list):
            if key in {
                "allowed",
                "email",
                "group",
                "service_account",
                "socket_ids",
                "cloud_authentication_email_allowed_addressses",
                "cloud_authentication_email_allowed_domains",
                "custom_domains",
            }:
                if key == "allowed":
                    kind = "text"
                elif "domain" in key:
                    kind = "hostname"
                else:
                    kind = "email" if "email" in key else "id"
                return [self.replacement(kind, item) for item in value]
            return [self.redact(item, key, parents) for item in value]
        if isinstance(value, dict):
            if "embedded_json" in parents:
                return {
                    str(self.replacement("embedded_key", child_key)): self.redact(
                        child, child_key, parents
                    )
                    for child_key, child in value.items()
                }
            if key == "tags":
                return {
                    f"tag:fixture-{index}": self.redact(item, tag, parents + (key,))
                    for index, (tag, item) in enumerate(sorted(value.items()), start=1)
                }
            return {
                child_key: self.redact(child, child_key, parents + (key,))
                for child_key, child in value.items()
            }

        if key in {"auth_info", "metadata"} and isinstance(value, str):
            try:
                decoded = json.loads(value)
            except json.JSONDecodeError:
                return self.replacement("text", value)
            return json.dumps(self.redact(decoded, key, parents + ("embedded_json",)), sort_keys=True)

        if key in {"client_port", "server_port"}:
            replacement = self.replacement("port", value)
            return str(replacement) if isinstance(value, str) else replacement
        if key in {"latitude", "longitude"}:
            return self.replacement(key, value)
        if not isinstance(value, str):
            return value

        if key == "id" or key.endswith("_id") or key in {"sub", "created_by", "service_account"}:
            return self.replacement("id", value)
        if key.endswith("email") or key in {"email", "user_email", "owner_email"}:
            return self.replacement("email", value)
        if key in {"picture", "image_url"}:
            return self.replacement("url", value)
        if key in {
            "client_ip",
            "ip",
            "ip_address",
            "private_network_ipv4",
        }:
            return self.replacement("ip", value)
        if key == "private_network_ipv6":
            return self.replacement("ipv6", value)
        if key in {
            "hostname",
            "dnsname",
            "upstream_http_hostname",
            "server_name",
            "subdomain",
        }:
            return self.replacement("hostname", value)
        if key in {
            "token",
            "access_token",
            "refresh_token",
            "bearer_token",
            "authorization",
            "password",
            "upstream_password",
            "protected_password",
            "private_key",
            "mtls_certificate",
            "ssh_public_key",
        }:
            return self.replacement("secret", value)
        if key in {"protected_username", "upstream_username", "username", "sshuser"}:
            return self.replacement("username", value)
        if key in {"command"}:
            return self.replacement("command", value)
        if key in {
            "name",
            "display_name",
            "connector_name",
            "database_name",
            "socket_name",
            "nickname",
        }:
            return self.replacement("name", value)
        if key in {"description", "country_code", "country_flag", "country_name", "city_name", "region_code", "region_name", "isp"}:
            return self.replacement(key, value)
        if "embedded_json" in parents or "tags" in parents:
            return self.replacement("text", value)
        if key in {
            "authentication_type",
            "completed_at",
            "created_at",
            "database_service_type",
            "end_time",
            "group_type",
            "last_seen",
            "last_seen_at",
            "protocol",
            "recording_type",
            "result",
            "role",
            "service_type",
            "session_type",
            "slug",
            "socket_type",
            "start_time",
            "status",
            "type",
            "updated_at",
            "upstream_type",
            "user_type",
        }:
            return value
        return self.replacement("text", value)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, default=Path(".capture"))
    parser.add_argument("--output", type=Path, default=Path("internal/b0api/testdata"))
    parser.add_argument("--allow-partial", action="store_true", help=argparse.SUPPRESS)
    args = parser.parse_args()

    if args.allow_partial:
        sources = sorted(args.input.glob("pam_*.json"))
    else:
        sources = [args.input / name for name in FIXTURE_NAMES]
        missing = [source.name for source in sources if not source.is_file()]
        if missing:
            raise SystemExit(f"missing PAM captures in {args.input}: {', '.join(missing)}")
    if not sources:
        raise SystemExit(f"no PAM captures found in {args.input}")
    if any(
        (args.output / source.name).resolve() == source.resolve()
        for source in sources
    ):
        raise SystemExit("--output must not overwrite an input capture")

    args.output.mkdir(parents=True, exist_ok=True)
    redactor = Redactor()
    for source in sources:
        document = json.loads(source.read_text())
        redacted = redactor.redact(document)
        (args.output / source.name).write_text(
            json.dumps(redacted, indent=2, sort_keys=True) + "\n"
        )


if __name__ == "__main__":
    main()
