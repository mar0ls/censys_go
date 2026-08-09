#!/usr/bin/env python3
"""
Generate docs/DOCUMENTATION.md from comments in main.go.

Extracts comment blocks (// ...) that immediately precede function and type
declarations and writes them as Markdown — grouped by category, with a table
of contents and summary tables, matching the project documentation style.
"""
import re
import unicodedata
from pathlib import Path

SRC = Path("main.go")
OUT_DIR = Path("docs")
OUT_FILE = OUT_DIR / "DOCUMENTATION.md"

# ── Category definitions ────────────────────────────────────────────────────
# Each entry: (display title, list of function/type names that belong here).
# Names listed here are matched case-sensitively against parsed identifiers.
# Anything not matched falls into "Other".
CATEGORIES = [
    (
        "Configuration",
        ["Config", "AppConfig", "loadConfig", "saveConfig", "validateConfig",
         "ensureConfig", "interactiveConfig", "createClient"],
    ),
    (
        "File handling & results",
        ["getHomeDir", "ensureResultsDir", "saveJSON", "askToSave"],
    ),
    (
        "Utilities",
        ["retryAPICall", "validateIP", "readLinesFromStdin", "getIPsFromUser",
         "parsePositiveInt", "showCredits"],
    ),
    (
        "Command handlers",
        ["handleSearch", "handleSingleView", "handleBulkView",
         "handleAggregate", "handleCertificate"],
    ),
]

# ── Parsing ──────────────────────────────────────────────────────────────────

def parse_source(src_text: str):
    """Return (package_comments, blocks).

    blocks is a list of dicts with keys: kind ('func'|'type'), name, comment.
    """
    lines = src_text.splitlines()
    blocks = []
    comment_buf: list[str] = []
    package_comments: list[str] = []
    saw_package = False

    func_re = re.compile(r"^\s*func\s*(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)")
    type_re = re.compile(r"^\s*type\s+([A-Za-z_][A-Za-z0-9_]*)")
    package_re = re.compile(r"^\s*package\s+\w+")

    for line in lines:
        stripped = line.lstrip()

        # Accumulate comment lines
        if stripped.startswith("//"):
            text = stripped[2:]
            if text.startswith(" "):
                text = text[1:]
            comment_buf.append(text)
            continue

        # Package declaration — grab preceding comments as package-level doc
        if not saw_package and package_re.match(line):
            saw_package = True
            if comment_buf:
                package_comments = comment_buf[:]
            comment_buf = []
            continue

        # Function declaration
        m = func_re.match(line)
        if m:
            blocks.append({
                "kind": "func",
                "name": m.group(1),
                "comment": "\n".join(comment_buf).strip(),
            })
            comment_buf = []
            continue

        # Type declaration
        m = type_re.match(line)
        if m:
            blocks.append({
                "kind": "type",
                "name": m.group(1),
                "comment": "\n".join(comment_buf).strip(),
            })
            comment_buf = []
            continue

        # Any other non-comment line resets the buffer
        comment_buf = []

    return package_comments, blocks


# ── Grouping ─────────────────────────────────────────────────────────────────

def group_blocks(blocks: list[dict]):
    """Return an ordered list of (category_title, [block, ...])."""
    by_name = {b["name"]: b for b in blocks}

    used: set[str] = set()
    grouped = []

    for title, names in CATEGORIES:
        members = [by_name[n] for n in names if n in by_name]
        if members:
            grouped.append((title, members))
            used.update(b["name"] for b in members)

    # Anything not explicitly categorised goes into "Other"
    remainder = [b for b in blocks if b["name"] not in used]
    if remainder:
        grouped.append(("Other", remainder))  # was "Inne" — fixed

    return grouped


# ── Helpers ───────────────────────────────────────────────────────────────────

def slugify(text: str) -> str:
    """Convert a section title to a GitHub-flavoured Markdown anchor slug.

    Handles Unicode by stripping diacritics, lowercasing, replacing spaces
    with hyphens, and dropping everything that isn't alphanumeric or a hyphen.
    Much more robust than a handful of manual .replace() calls.
    """
    # Normalise to NFD so accented chars decompose (é → e + combining accent)
    nfd = unicodedata.normalize("NFD", text)
    # Drop combining characters (the accent parts)
    ascii_text = "".join(c for c in nfd if unicodedata.category(c) != "Mn")
    lower = ascii_text.lower()
    # Replace spaces and & with hyphens
    slug = re.sub(r"[\s&]+", "-", lower)
    # Drop anything that isn't a letter, digit, or hyphen
    slug = re.sub(r"[^a-z0-9\-]", "", slug)
    # Collapse multiple hyphens
    slug = re.sub(r"-+", "-", slug).strip("-")
    return "#" + slug


# ── Rendering ─────────────────────────────────────────────────────────────────

def _signature(block: dict) -> str:
    """Return display name: 'TypeName' for types, 'funcName()' for funcs."""
    if block["kind"] == "type":
        return block["name"]
    return f"{block['name']}()"


def render_md(pkg_comments: list[str], blocks: list[dict]):
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    grouped = group_blocks(blocks)

    # Build table-of-contents entries
    toc_entries = []
    if pkg_comments:
        toc_entries.append(("Package overview", "#package-overview"))
    for title, _ in grouped:
        # was broken for non-ASCII
        toc_entries.append((title, slugify(title)))  

    with OUT_FILE.open("w", encoding="utf-8") as f:

        # Header
        f.write("# Censys-Go — CLI Documentation\n\n")
        f.write('<div id="header" align="center">\n')
        f.write('    <img src="https://media3.giphy.com/media/v1.Y2lkPTc5MGI3NjExcnlxcXUxaHhsa2J0N3ZranM2a3RxaXUyaWRpZW96bHoxY2poaXJ3bCZlcD12MV9pbnRlcm5hbF9naWZfYnlfaWQmY3Q9Zw/q15lIdQWBYs7K/giphy.gif" width="200"/>\n')
        f.write('</div>\n\n')
        # Table of contents
        f.write("## Table of contents\n\n")
        for idx, (label, anchor) in enumerate(toc_entries, 1):
            f.write(f"{idx}. [{label}]({anchor})\n")
        f.write("\n---\n\n")

        # Package-level overview
        if pkg_comments:
            f.write("## Package overview\n\n")
            f.write("\n".join(pkg_comments) + "\n\n---\n\n")

        # Grouped sections
        for title, members in grouped:
            f.write(f"## {title}\n\n")

            # Summary table
            f.write("| Function / Type | Description |\n")
            f.write("|-----------------|-------------|\n")
            for b in members:
                sig = _signature(b)
                desc = b["comment"].splitlines()[0] if b["comment"] else "_No description provided._"
                desc = desc.replace("|", "\\|")
                f.write(f"| `{sig}` | {desc} |\n")
            f.write("\n")

            # Detailed entries
            for b in members:
                sig = _signature(b)
                f.write(f"### `{sig}`\n\n")
                if b["comment"]:
                    f.write(b["comment"] + "\n\n")
                else:
                    f.write("_No comment provided._\n\n")

            f.write("---\n\n")

    print(f"Generated {OUT_FILE}")


# ── Entry point ───────────────────────────────────────────────────────────────

def main() -> int:
    if not SRC.exists():
        print(f"Error: source file '{SRC}' not found.")
        return 1

    text = SRC.read_text(encoding="utf-8")
    pkg_comments, blocks = parse_source(text)

    if not blocks:
        print("Warning: no functions or types found in source.")

    render_md(pkg_comments, blocks)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())