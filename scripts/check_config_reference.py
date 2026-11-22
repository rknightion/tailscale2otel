#!/usr/bin/env python3
"""Check the configuration reference through the pinned site renderer."""

import html.parser
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent
ZENSICAL = "zensical==0.0.23"


class ReferenceHTML(html.parser.HTMLParser):
    """Extract visible article text, headings and real table body cells."""

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.article = False
        self.pre = 0
        self.heading = False
        self.table = False
        self.body = False
        self.cell = False
        self.anchor = False
        self.buffer = ""
        self.lines = []

    def handle_starttag(self, tag, attrs):
        if tag == "article":
            self.article = True
        if not self.article:
            return
        if tag == "pre":
            self.pre += 1
        if self.pre:
            return
        if tag == "a" and self.heading:
            self.anchor = True
        elif tag in ("h1", "h2", "h3", "h4", "h5", "h6"):
            self.flush()
            self.heading = True
            self.buffer = "# "
        elif tag == "table":
            self.flush()
            self.table = True
        elif tag == "tbody" and self.table:
            self.body = True
        elif tag == "td" and self.body:
            self.cell = True
            self.buffer = ""
        elif tag == "code" and not self.anchor:
            self.buffer += "`"

    def handle_endtag(self, tag):
        if not self.article:
            return
        if tag == "pre":
            self.pre -= 1
            return
        if self.pre:
            return
        if tag == "a" and self.anchor:
            self.anchor = False
        elif tag in ("h1", "h2", "h3", "h4", "h5", "h6"):
            self.flush()
            self.heading = False
        elif tag == "td" and self.cell:
            # Only the first cell of a row can carry relative keys.
            if not self.row_cell:
                self.lines.append("| " + self.buffer.replace("\n", " ") + " |")
            else:
                self.lines.append("text: " + self.buffer.replace("\n", " "))
            self.row_cell = True
            self.cell = False
            self.buffer = ""
        elif tag == "tr" and self.body:
            self.row_cell = False
        elif tag == "tbody":
            self.body = False
        elif tag == "table":
            self.table = False
        elif tag == "code" and not self.anchor:
            self.buffer += "`"
        elif tag == "article":
            self.flush()
            self.article = False
        elif tag in ("p", "li", "div") and not self.table:
            self.flush()

    def handle_data(self, data):
        if self.article and not self.pre and not self.anchor and (not self.table or self.cell):
            self.buffer += data

    def flush(self):
        text = " ".join(self.buffer.split())
        if text:
            self.lines.append(text if self.heading else "text: " + text)
        self.buffer = ""


def run():
    with tempfile.TemporaryDirectory(prefix="config-reference-") as tmp:
        tmp = Path(tmp)
        envdir = tmp / "venv"
        subprocess.run([sys.executable, "-m", "venv", str(envdir)], check=True)
        python = envdir / "bin" / "python"
        subprocess.run([str(python), "-m", "pip", "install", "--disable-pip-version-check", ZENSICAL], check=True)
        project = tmp / "project"
        docs = project / "docs"
        docs.mkdir(parents=True)
        (project / "zensical.toml").write_text('[project]\nsite_name = "Config reference check"\n')
        original = (ROOT / "docs/configuration.md").read_text()
        heading = "#### `collectors.node_metrics.discovery`\n"
        if original.count(heading) != 1:
            raise ValueError("discovery control heading changed; update the renderer control")
        start = original.index(heading) + len(heading)
        table = original.index("| Key | Default | Description |", start)
        malformed = original
        if original[table - 2:table] == "\n\n":
            malformed = original[:table - 1] + original[table:]
        (docs / "index.md").write_text(original)
        (docs / "malformed.md").write_text(malformed)
        (docs / "forms.md").write_text('''# Forms

`first.enabled` and `collectors.flowlogs.objectstore.bucket`.
`collectors.auditlogs.objectstore.*` and `collectors.k8s_audit.objectstore.*`.

| Key | Description |
| --- | --- |
| `streaming.tls.cert_file` / `.key_file` | TLS files |

#### `collector.discovery`

| Key | Description |
| --- | --- |
| interval | Poll period |
| enabled | Another shared leaf |
''')
        subprocess.run([str(envdir / "bin" / "zensical"), "build"], cwd=project, check=True)
        for name, source in (("reference", project / "site/index.html"),
                             ("malformed", project / "site/malformed/index.html"),
                             ("forms", project / "site/forms/index.html")):
            parser = ReferenceHTML()
            parser.row_cell = False
            parser.feed(source.read_text())
            (tmp / (name + ".txt")).write_text("\n".join(parser.lines) + "\n")
        env = os.environ.copy()
        env["TS2OTEL_RENDERED_CONFIG_REFERENCE"] = str(tmp / "reference.txt")
        env["TS2OTEL_RENDERED_CONFIG_CONTROLS"] = str(tmp)
        subprocess.run(["go", "test", "-count=1", "-timeout=90s", "-run", "^TestDocsConfigurationMentionsEveryKey$", "./internal/config/"], cwd=ROOT, env=env, check=True)


if __name__ == "__main__":
    started = time.monotonic()
    try:
        run()
    finally:
        print(f"configuration reference renderer: {time.monotonic() - started:.2f}s", flush=True)
