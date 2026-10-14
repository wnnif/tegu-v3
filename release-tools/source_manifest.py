#!/usr/bin/env python3
"""Generate or verify a SHA-256 manifest for tracked Git checkout files."""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import subprocess
import sys
from pathlib import Path


MANIFEST_NAME = "SOURCE-MANIFEST.sha256"
ENTRY_RE = re.compile(r"^([0-9a-f]{64})  (.+)$")


def tracked_paths(root: Path) -> list[str]:
    result = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z"],
        check=True,
        stdout=subprocess.PIPE,
    )
    paths = [os.fsdecode(item) for item in result.stdout.split(b"\0") if item]
    return sorted(path for path in paths if path != MANIFEST_NAME)


def validate_relative_path(path: str) -> None:
    parts = path.split("/")
    if path.startswith("/") or "\\" in path or any(part in ("", ".", "..") for part in parts):
        raise ValueError(f"unsafe manifest path: {path!r}")


def digest_file(root: Path, relative: str) -> str:
    validate_relative_path(relative)
    path = root.joinpath(*relative.split("/"))
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"tracked path is not a regular file: {relative}")
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def digest_index_file(root: Path, relative: str) -> str:
    validate_relative_path(relative)
    result = subprocess.run(
        ["git", "-C", str(root), "show", f":{relative}"],
        check=True,
        stdout=subprocess.PIPE,
    )
    return hashlib.sha256(result.stdout).hexdigest()


def write_manifest(root: Path) -> None:
    lines = [f"{digest_index_file(root, path)}  {path}" for path in tracked_paths(root)]
    (root / MANIFEST_NAME).write_bytes(("\n".join(lines) + "\n").encode("utf-8"))
    print(f"Wrote {MANIFEST_NAME}: {len(lines)} tracked files")


def read_manifest(root: Path) -> dict[str, str]:
    raw = (root / MANIFEST_NAME).read_bytes()
    text = raw.decode("utf-8")
    if b"\r" in raw or not text.endswith("\n"):
        raise ValueError(f"{MANIFEST_NAME} must use LF and end with a newline")
    entries: dict[str, str] = {}
    for line in text[:-1].split("\n"):
        match = ENTRY_RE.fullmatch(line)
        if not match:
            raise ValueError(f"invalid manifest line: {line!r}")
        digest, relative = match.groups()
        validate_relative_path(relative)
        if relative == MANIFEST_NAME or relative in entries:
            raise ValueError(f"duplicate or self-referential manifest path: {relative}")
        entries[relative] = digest
    return entries


def check_manifest(root: Path) -> None:
    expected = set(tracked_paths(root))
    listed = read_manifest(root)
    actual = set(listed)
    missing = sorted(expected - actual)
    extra = sorted(actual - expected)
    failures = [f"missing: {path}" for path in missing] + [f"untracked entry: {path}" for path in extra]
    for relative in sorted(expected & actual):
        digest = digest_file(root, relative)
        if digest != listed[relative]:
            failures.append(f"SHA-256 mismatch: {relative}")
    if failures:
        raise ValueError("\n".join(failures))
    print(f"PASS: {len(expected)} tracked files match {MANIFEST_NAME}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--write", action="store_true", help="write the checkout manifest")
    mode.add_argument("--check", action="store_true", help="verify coverage and SHA-256 values")
    parser.add_argument("--root", type=Path, default=Path.cwd(), help="Git checkout root")
    args = parser.parse_args()
    root = args.root.resolve()
    try:
        if args.write:
            write_manifest(root)
        else:
            check_manifest(root)
    except (OSError, subprocess.CalledProcessError, UnicodeError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
