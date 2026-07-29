#!/usr/bin/env python3
"""Fetch the exact public Qwen3.6-35B-A3B J-lens and verify its digest."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path

from huggingface_hub import hf_hub_download


DEFAULT_REPOSITORY = "stanleytheli/qwen3.6-35B-A3B-jlens"
DEFAULT_REVISION = "7a5dc7a6c770c272226a321409b30d7e6d773bba"


def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(8 << 20), b""):
            value.update(chunk)
    return value.hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", default=DEFAULT_REPOSITORY)
    parser.add_argument("--revision", default=DEFAULT_REVISION)
    parser.add_argument("--filename", default="lens.pt")
    parser.add_argument(
        "--destination",
        default=str(Path.home() / ".j" / "jspace" / "lenses" / "qwen3.6-35B-A3B" / "lens.pt"),
    )
    arguments = parser.parse_args()
    destination = Path(arguments.destination).expanduser().resolve()
    destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    downloaded = Path(
        hf_hub_download(
            repo_id=arguments.repo,
            filename=arguments.filename,
            revision=arguments.revision,
            local_dir=destination.parent,
        )
    )
    if downloaded != destination:
        os.replace(downloaded, destination)
    destination.chmod(0o600)
    print(
        json.dumps(
            {
                "repository": arguments.repo,
                "revision": arguments.revision,
                "filename": arguments.filename,
                "path": str(destination),
                "bytes": destination.stat().st_size,
                "sha256": digest(destination),
            },
            separators=(",", ":"),
        )
    )


if __name__ == "__main__":
    main()
