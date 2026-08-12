#!/usr/bin/env python3
"""
Generate docs/DOCUMENTATION.md from the doc comments in the Go sources.

Walks main.go and internal/**, extracts the comment block preceding each
function, type, and package-level const/var declaration, and writes them as
Markdown grouped by package. Test files are skipped.

Run from the repository root:

    python3 scripts/generate_docs.py
"""
from __future__ import annotations

import re
import unicodedata
from pathlib import Path

ROOT = Path(".")
OUT_FILE = Path("docs") / "DOCUMENTATION.md"
HEADER_IMAGE = (
    "https://media3.giphy.com/media/v1.Y2lkPTc5MGI3NjExcnlxcXUxaHhsa2J0N3ZranM2a3RxaXUyaWRpZW96bHoxY2poaXJ3bCZlcD12MV9pbnRlcm5hbF9naWZfYnlfaWQmY3Q9Zw/q15lIdQWBYs7K/giphy.gif"
)

# Package directories in the order they should appear. Anything else found on
# disk is appended afterwards, so a new package shows up without editing this.
PACKAGE_ORDER = [
    ".",
    "internal/cli",
    "internal/censysx",
    "internal/config",
    "internal/render",
    "internal/hunt",
    "internal/ui",
]

# ── Parsing ──────────────────────────────────────────────────────────────────

FUNC_RE = re.compile(r"^func\s*(?:\(\s*\w+\s+\*?(\w+)\s*\)\s*)?([A-Za-z_]\w*)")
TYPE_RE = re.compile(r"^type\s+([A-Za-z_]\w*)")
DECL_RE = re.compile(r"^(const|var)\s+(?:\(|([A-Za-z_]\w*))")
PACKAGE_RE = re.compile(r"^package\s+(\w+)")


def parse_file(path: Path) -> tuple[str, list[str], list[dict]]:
    """Return (package name, package doc lines, declaration blocks)."""
    package = ""
    package_doc: list[str] = []
    blocks: list[dict] = []

    comment: list[str] = []
    seen_package = False

    for line in path.read_text(encoding="utf-8").splitlines():
        stripped = line.strip()

        if stripped.startswith("//"):
            text = stripped[2:]
            comment.append(text[1:] if text.startswith(" ") else text)
            continue

        # Only top-level declarations carry documentation worth extracting;
        # anything indented is a struct field or a statement.
        if line[:1].isspace() or not stripped:
            if not stripped:
                comment = []
            continue

        match = PACKAGE_RE.match(stripped)
        if match and not seen_package:
            seen_package = True
            package = match.group(1)
            package_doc = comment[:]
            comment = []
            continue

        match = FUNC_RE.match(stripped)
        if match:
            receiver, name = match.group(1), match.group(2)
            blocks.append({
                "kind": "func",
                "name": f"{receiver}.{name}" if receiver else name,
                "comment": "\n".join(comment).strip(),
            })
            comment = []
            continue

        match = TYPE_RE.match(stripped)
        if match:
            blocks.append({"kind": "type", "name": match.group(1), "comment": "\n".join(comment).strip()})
            comment = []
            continue

        match = DECL_RE.match(stripped)
        if match:
            name = match.group(2) or f"{match.group(1)} block"
            blocks.append({"kind": match.group(1), "name": name, "comment": "\n".join(comment).strip()})
            comment = []
            continue

        comment = []

    return package, package_doc, blocks


def collect_packages() -> list[dict]:
    """Return one entry per package directory that holds Go sources."""
    directories = list(PACKAGE_ORDER)
    for path in sorted(ROOT.glob("internal/*")):
        key = path.as_posix()
        if path.is_dir() and key not in directories:
            directories.append(key)

    packages = []
    for directory in directories:
        sources = sorted(
            p for p in Path(directory).glob("*.go")
            if not p.name.endswith("_test.go")
        )
        if not sources:
            continue

        name, doc, blocks = "", [], []
        for source in sources:
            pkg_name, pkg_doc, file_blocks = parse_file(source)
            name = name or pkg_name
            # The package comment lives on whichever file carries it.
            doc = doc or pkg_doc
            blocks.extend(file_blocks)

        packages.append({
            "name": name,
            "path": directory,
            "doc": doc,
            "blocks": [b for b in blocks if b["comment"]],
            "files": [p.as_posix() for p in sources],
        })
    return packages


# ── Rendering ────────────────────────────────────────────────────────────────

def slugify(text: str) -> str:
    """Convert a heading to a GitHub-flavoured Markdown anchor."""
    nfd = unicodedata.normalize("NFD", text)
    ascii_text = "".join(c for c in nfd if unicodedata.category(c) != "Mn")
    slug = re.sub(r"[\s&/]+", "-", ascii_text.lower())
    slug = re.sub(r"[^a-z0-9\-]", "", slug)
    return "#" + re.sub(r"-+", "-", slug).strip("-")


def signature(block: dict) -> str:
    return f"{block['name']}()" if block["kind"] == "func" else block["name"]


def render(packages: list[dict]) -> None:
    OUT_FILE.parent.mkdir(parents=True, exist_ok=True)

    with OUT_FILE.open("w", encoding="utf-8") as f:
        f.write("# Censys-Go — CLI Documentation\n\n")
        f.write('<div id="header" align="center">\n')
        f.write(f'    <img src="{HEADER_IMAGE}" width="200"/>\n')
        f.write("</div>\n\n")
        f.write("_Generated by `scripts/generate_docs.py`. Edit the Go doc comments, not this file._\n\n")

        f.write("## Table of contents\n\n")
        for index, pkg in enumerate(packages, 1):
            heading = f"Package `{pkg['name']}`"
            f.write(f"{index}. [{heading}]({slugify(heading)}) — `{pkg['path']}`\n")
        f.write("\n---\n\n")

        for pkg in packages:
            f.write(f"## Package `{pkg['name']}`\n\n")

            if pkg["doc"]:
                f.write("\n".join(pkg["doc"]) + "\n\n")

            f.write(f"Directory: `{pkg['path']}`\n\n")
            f.write("Files: " + ", ".join(f"`{Path(name).name}`" for name in pkg["files"]) + "\n\n")

            if not pkg["blocks"]:
                f.write("_No documented declarations._\n\n---\n\n")
                continue

            f.write("| Declaration | Description |\n|---|---|\n")
            for block in pkg["blocks"]:
                summary = block["comment"].splitlines()[0].replace("|", "\\|")
                f.write(f"| `{signature(block)}` | {summary} |\n")
            f.write("\n")

            for block in pkg["blocks"]:
                f.write(f"### `{signature(block)}`\n\n{block['comment']}\n\n")

            f.write("---\n\n")

    print(f"Generated {OUT_FILE}")


def main() -> int:
    packages = collect_packages()
    if not packages:
        print("Error: no Go sources found; run this from the repository root.")
        return 1

    render(packages)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
