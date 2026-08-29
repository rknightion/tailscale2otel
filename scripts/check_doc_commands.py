#!/usr/bin/env python3
"""Validate the install commands in README.md and docs/ against the real schemas (#445).

WHY THIS EXISTS. The install commands in the README are the first thing anyone
runs, and nothing checked them. `deploy/helm/tests/render-tests.sh` renders the
chart from `--set` flags constructed inside the script, so it proves the chart
works — it never reads the command a user would actually copy. CI runs no command
taken from any documentation file.

The failure that motivated it had been sitting in the README, verbatim, for
months:

    --set-string secrets.TS2OTEL_TAILSCALE__AUTH__OAUTH__CLIENT_ID=<client-id>

The chart's key is `secret`, singular. Helm accepts unknown `--set` paths
silently, so the documented quick start installs the chart with NO credentials
and the exporter starts, fails to authenticate, and reports an auth error that
looks like a bad OAuth client rather than a typo in the docs.

WHAT IT CHECKS, and why these checks specifically. Each is a case where the WRONG
value is accepted silently and only surfaces as a confusing runtime symptom:

  1. Every `--set`/`--set-string` key in a documented `helm` command resolves
     against `values.schema.json` — the generated, authoritative description of
     what the chart accepts.
  2. Every `TS2OTEL_*` variable in a documented `docker run -e` resolves against
     `docs/env-vars.md` — the generated reference of every real variable. A
     mistyped variable is simply ignored by the config loader, so the exporter
     starts with a default nobody intended.

  3. No documented `helm` command puts a CREDENTIAL inline on the command line
     (#341). `--set secret.TS2OTEL_..._TOKEN=<token>` works, which is exactly
     why it survived: the credential lands in the operator's shell history and
     is visible in `ps` output to every other user on the machine for the
     duration of the install. This is not a typo class — the command does what
     it says — so nothing else in this file would ever have caught it. The
     credential surface is derived from the chart's own authoritative list
     (`tailscale2otel.credentialPaths` in `_helpers.tpl`) plus everything under
     `secret.`, which is by definition the credential env map, so adding a new
     credential field to the chart extends this check with no edit here.

     `--set-file` and `-f/--values` are the supported alternatives and are
     allowed: the value comes from a file rather than argv. `--set-file` keys
     are validated against the schema like any other, because it was previously
     invisible to the extractor.

  4. Every declared social-image path is either a real file under `docs/` or
     the exact asset injected by the m7kni.io hub. The latter is deliberately
     git-ignored in this child repository; any other missing path is a broken
     share preview and fails here instead of only after the hub build.

WHAT IT DOES NOT DO. It does not execute anything. Running `helm install` or
`docker run` from CI would need a cluster or a registry pull, and — the reason
the issue calls it out — must never touch a real control plane. Checking the
commands against the schemas catches the class of error that actually occurs
(a key that does not exist) without any of that.

Usage:
    scripts/check_doc_commands.py            # check commands + metadata assets
    scripts/check_doc_commands.py --list     # print what was extracted, then check

Exit codes:
    0  every documented key and metadata asset resolves
    1  a documented command or metadata declaration references something that does not exist
    2  a schema or manifest this depends on could not be read
"""

import argparse
import glob
import json
import os
import re
import subprocess
import sys
import tomllib

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCHEMA = os.path.join(REPO, "deploy", "helm", "tailscale2otel", "values.schema.json")
ENV_DOC = os.path.join(REPO, "docs", "env-vars.md")
HELPERS = os.path.join(REPO, "deploy", "helm", "tailscale2otel", "templates", "_helpers.tpl")
MANIFEST = os.path.join(REPO, "docs.toml")

# The hub injects these files into a throwaway child clone immediately before
# generating zensical.toml. They are deliberately absent from a child checkout
# and ignored by git; accepting any other missing path would turn a typo in the
# manifest into a successful build with a broken og:image.
HUB_SOCIAL_IMAGES = frozenset(("assets/social-card.png", "assets/social-card.svg"))

# Front matter is the only page-level place where an image declaration is
# meaningful. Restricting this scan to the opening YAML block avoids treating a
# prose example or a Markdown image link as a metadata declaration.
FRONT_MATTER_RE = re.compile(r"\A---\r?\n(.*?)\r?\n---(?:\r?\n|$)", re.S)
IMAGE_RE = re.compile(
    r"^\s*image\s*:\s*(?:['\"]([^'\"]+)['\"]|(\S+?))(?:\s+#.*)?\s*$", re.M
)

# --set/--set-string flags, quoted or bare, up to the '='. NOT --set-file: that
# reads the value from a file rather than argv, which is the whole point of
# allowing it, so it is extracted separately by SET_FILE_RE below.
SET_RE = re.compile(r"--set(?:-string)?[= ]+[\"']?([A-Za-z0-9_.\[\]-]+)=")
# --set-file keys. Schema-validated like any other key; never a credential
# exposure, because the value never appears on the command line.
SET_FILE_RE = re.compile(r"--set-file[= ]+[\"']?([A-Za-z0-9_.\[\]-]+)=")
# The chart's authoritative credential list, between the define and its end.
CRED_BLOCK_RE = re.compile(
    r'{{-\s*define\s+"tailscale2otel\.credentialPaths"\s*-}}(.*?){{-\s*end\s*-}}', re.S)
# -e / --env NAME=..., restricted to this project's prefix.
ENV_RE = re.compile(r"(?:-e|--env)[= ]+[\"']?(TS2OTEL_[A-Z0-9_]+)")
# `kubectl create secret ... --from-literal=TS2OTEL_X=value`. Same exposure as
# an inline --set and the obvious way to write the existingSecret quick start
# wrongly, so it is screened too; --from-file/--from-env-file are the safe forms.
FROM_LITERAL_RE = re.compile(r"--from-literal[= ]+[\"']?(TS2OTEL_[A-Z0-9_]+)=")
# Env-var reference rows: | `TS2OTEL_FOO` | ... |
ENV_DOC_RE = re.compile(r"^\|\s*`(TS2OTEL_[A-Z0-9_]+)`\s*\|", re.M)


def die(msg):
    print("check_doc_commands: %s" % msg, file=sys.stderr)
    raise SystemExit(2)


def load_schema_paths():
    """Every dotted value path the chart's schema declares.

    Object nodes with no declared `properties` (free-form maps such as
    `podAnnotations`) accept anything, so they are recorded as open prefixes —
    validating a user-chosen key inside one would be wrong.
    """
    try:
        with open(SCHEMA, encoding="utf-8") as f:
            doc = json.load(f)
    except (OSError, ValueError) as exc:
        die("cannot read %s: %s (run just gen helm)" % (SCHEMA, exc))

    known, open_prefixes = set(), set()

    def walk(node, prefix):
        if not isinstance(node, dict):
            return
        props = node.get("properties")
        if node.get("type") == "object" and not props:
            if prefix:
                open_prefixes.add(prefix)
            return
        for name, child in (props or {}).items():
            path = "%s.%s" % (prefix, name) if prefix else name
            known.add(path)
            walk(child, path)
        items = node.get("items")
        if isinstance(items, dict) and prefix:
            open_prefixes.add(prefix)

    walk(doc, "")
    return known, open_prefixes


def load_known_env():
    try:
        with open(ENV_DOC, encoding="utf-8") as f:
            body = f.read()
    except OSError as exc:
        die("cannot read %s: %s" % (ENV_DOC, exc))
    names = set(ENV_DOC_RE.findall(body))
    if not names:
        die("%s lists no TS2OTEL_* variables; the reference table may have changed shape, "
            "and an empty set here would make every check below pass vacuously" % ENV_DOC)
    return names


def load_credential_prefixes():
    """Chart value prefixes whose value is a credential.

    Derived from `tailscale2otel.credentialPaths` — the chart's own list, which
    `deploy/CLAUDE.md` names as authoritative and which every template routes
    through — so a new credential field added to the chart is covered here
    without touching this file. `secret.` is added because the whole point of
    that map is TS2OTEL_* credential env vars.
    """
    try:
        with open(HELPERS, encoding="utf-8") as f:
            body = f.read()
    except OSError as exc:
        die("cannot read %s: %s" % (HELPERS, exc))
    m = CRED_BLOCK_RE.search(body)
    if not m:
        die("%s no longer defines tailscale2otel.credentialPaths in the expected shape; "
            "an empty credential set would make the inline-credential check pass vacuously"
            % HELPERS)
    paths = [p.strip() for p in m.group(1).split("\n") if p.strip()]
    if not paths:
        die("tailscale2otel.credentialPaths parsed as empty; refusing to check nothing")
    prefixes = {"secret"}
    prefixes.update("config.%s" % p for p in paths)
    return prefixes


def load_declared_social_images():
    """Return (source, field, path) for every declared social-image path.

    ``docs.toml`` is the hub manifest and owns the site-wide social image. A
    page's front matter can also carry an ``image`` key, so inspect those
    declarations too. The old root ``docs/.meta.yml`` was not consumed by the
    hub for site metadata; if one is reintroduced, its image declaration is
    still checked rather than allowed to fail silently.
    """
    try:
        with open(MANIFEST, "rb") as f:
            manifest = tomllib.load(f)
    except (OSError, tomllib.TOMLDecodeError) as exc:
        die("cannot read %s: %s" % (MANIFEST, exc))

    declarations = []
    assets = manifest.get("assets", {})
    if assets is None:
        assets = {}
    if not isinstance(assets, dict):
        die("%s has a non-table [assets] value" % MANIFEST)
    if "social_image" in assets:
        declarations.append(("docs.toml", "assets.social_image", assets["social_image"]))

    candidates = [os.path.join(REPO, "docs", ".meta.yml")]
    candidates += sorted(glob.glob(os.path.join(REPO, "docs", "**", "*.md"), recursive=True))
    for path in candidates:
        if not os.path.isfile(path):
            continue
        try:
            with open(path, encoding="utf-8") as f:
                body = f.read()
        except (OSError, UnicodeDecodeError) as exc:
            die("cannot read metadata file %s: %s" % (path, exc))
        if path.endswith(".md"):
            match = FRONT_MATTER_RE.match(body)
            body = match.group(1) if match else ""
        rel = os.path.relpath(path, REPO)
        for match in IMAGE_RE.finditer(body):
            declarations.append((rel, "image", match.group(1) or match.group(2)))
    return declarations


def hub_injected_asset(path):
    """Whether a missing path is the exact social asset the hub injects.

    Checking git's ignore rules makes the generated-file exception explicit:
    the child must not commit a copy, but a future change that removes the
    ignore contract cannot quietly make this check pass.
    """
    if path not in HUB_SOCIAL_IMAGES:
        return False
    target = os.path.join(REPO, "docs", path)
    try:
        result = subprocess.run(
            ["git", "check-ignore", "--quiet", "--", target],
            cwd=REPO,
            check=False,
        )
    except OSError as exc:
        die("cannot verify hub-generated asset %s with git check-ignore: %s" % (path, exc))
    if result.returncode == 0:
        return True
    if result.returncode == 1:
        return False
    die("cannot verify hub-generated asset %s: git check-ignore exited %d"
        % (path, result.returncode))


def check_social_images():
    """Return (number checked, problems) for declared social-image paths."""
    checked = 0
    problems = []
    docs_root = os.path.realpath(os.path.join(REPO, "docs"))
    for source, field, raw_path in load_declared_social_images():
        checked += 1
        if not isinstance(raw_path, str) or not raw_path.strip():
            problems.append((source, field, "declared social-image path is empty"))
            continue
        path = raw_path.strip()
        if (path.startswith(("/", "//")) or re.match(r"^[A-Za-z][A-Za-z0-9+.-]*://", path)
                or "\\" in path):
            problems.append((source, "%s=%s" % (field, path),
                             "social-image metadata must name a repository-relative asset"))
            continue
        target = os.path.realpath(os.path.join(docs_root, path))
        try:
            inside_docs = os.path.commonpath((docs_root, target)) == docs_root
        except ValueError:
            inside_docs = False
        if not inside_docs:
            problems.append((source, "%s=%s" % (field, path),
                             "social-image path escapes the docs directory"))
            continue
        if path in HUB_SOCIAL_IMAGES:
            if hub_injected_asset(path):
                continue
            if os.path.isfile(target):
                problems.append((source, "%s=%s" % (field, path),
                                 "hub-generated asset exists but is not git-ignored"))
                continue
        elif os.path.isfile(target):
            continue
        problems.append((source, "%s=%s" % (field, path),
                         "declared social-image asset is missing; add the asset or remove "
                         "unsupported metadata"))
    return checked, problems


def is_credential(key, prefixes):
    clean = re.sub(r"\[\d+\]", "", key)
    return any(clean == p or clean.startswith(p + ".") for p in prefixes)


def doc_files():
    files = [os.path.join(REPO, "README.md")]
    files += sorted(glob.glob(os.path.join(REPO, "docs", "*.md")))
    # NOTES.txt is the post-install text every `helm install` prints, so it is a
    # documented command surface exactly like the README — and it taught inline
    # `--set secret.*` for just as long (#341). It is a Go template, but the
    # command lines in it are literal, so the same extractors apply.
    files.append(os.path.join(REPO, "deploy", "helm", "tailscale2otel", "templates", "NOTES.txt"))
    return files


def extract(path):
    """(helm --set keys, docker -e vars) referenced anywhere in a document.

    Deliberately scans the whole file rather than only fenced blocks: docs/ uses
    MkDocs tabbed sections whose fences are indented four spaces, and a
    column-zero fence matcher silently finds nothing in exactly the file that
    carries the most install commands.
    """
    with open(path, encoding="utf-8") as f:
        body = f.read()
    return (SET_RE.findall(body), ENV_RE.findall(body),
            SET_FILE_RE.findall(body), FROM_LITERAL_RE.findall(body))


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--list", action="store_true", help="print everything extracted")
    args = ap.parse_args()

    known_paths, open_prefixes = load_schema_paths()
    known_env = load_known_env()
    cred_prefixes = load_credential_prefixes()
    social_images_checked, metadata_problems = check_social_images()

    def resolves(key):
        # Strip list indices; the schema describes the element, not the slot.
        clean = re.sub(r"\[\d+\]", "", key)
        if clean in known_paths:
            return True
        return any(clean == p or clean.startswith(p + ".") for p in open_prefixes)

    problems, checked = [], 0
    inline_cred_checked = 0
    for path in doc_files():
        rel = os.path.relpath(path, REPO)
        set_keys, env_vars, set_file_keys, literals = extract(path)
        for var in literals:
            inline_cred_checked += 1
            problems.append((rel, "--from-literal %s=<value>" % var,
                             "puts a CREDENTIAL inline on the command line, same exposure as "
                             "--set. Use --from-file or --from-env-file with a mode-0600 file "
                             "(#341)"))
        for key in set_keys + set_file_keys:
            checked += 1
            if not resolves(key):
                problems.append((rel, "--set %s" % key,
                                 "no such chart value; helm accepts unknown --set paths "
                                 "SILENTLY, so this documented command installs without it"))
        for key in set_keys:
            inline_cred_checked += 1
            if is_credential(key, cred_prefixes):
                problems.append((rel, "--set %s=<value>" % key,
                                 "puts a CREDENTIAL inline on the command line, where it lands "
                                 "in shell history and is visible in `ps` to every other user on "
                                 "the machine. Document a pre-created existingSecret, --set-file, "
                                 "or -f with a mode-0600 values file instead (#341)"))
        for var in env_vars:
            checked += 1
            if var not in known_env:
                problems.append((rel, "-e %s" % var,
                                 "not a real TS2OTEL_* variable; the config loader ignores "
                                 "unknown variables, so this silently takes the default"))
        if args.list and (set_keys or env_vars or set_file_keys):
            print("%s: %d --set keys, %d --set-file keys, %d env vars"
                  % (rel, len(set_keys), len(set_file_keys), len(env_vars)))

    print("checked %d documented keys across %d files (%d --set keys screened for inline "
          "credentials against %d credential prefixes; %d social-image declarations checked)"
          % (checked, len(doc_files()), inline_cred_checked, len(cred_prefixes),
             social_images_checked))
    if not inline_cred_checked:
        print("check_doc_commands: no --set key was screened for inline credentials — the "
              "extractor is broken or the docs carry no helm commands; either way the #341 "
              "check passed vacuously and this is not a pass", file=sys.stderr)
        return 1
    if not checked:
        print("check_doc_commands: extracted nothing at all — the extractor is broken or the "
              "docs no longer carry install commands, either way this is not a pass",
              file=sys.stderr)
        return 1

    problems += metadata_problems
    for rel, what, why in problems:
        print("  %s: %s — %s" % (rel, what, why), file=sys.stderr)
    if problems:
        print("\n%d documented command or metadata declaration(s) failed validation."
              % len(problems), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
