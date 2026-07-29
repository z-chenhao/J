#!/usr/bin/env python3
"""Small stdio MCP server for the J Package memory example."""

from __future__ import annotations

import json
import os
import re
import sys
import time
import uuid
from pathlib import Path
from typing import Any


SECRET_PATTERNS = (
    re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"),
    re.compile(r"\b(?:sk|ghp|github_pat)_[A-Za-z0-9_-]{20,}\b"),
    re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b"),
)


class MemoryStore:
    """Append-only JSONL memory with explicit tombstones."""

    def __init__(self, path: Path):
        self.path = path.expanduser().resolve()

    def store(self, content: str, tags: list[str] | None = None) -> dict[str, Any]:
        content = content.strip()
        if not content:
            raise ValueError("content must not be empty")
        if any(pattern.search(content) for pattern in SECRET_PATTERNS):
            raise ValueError("content appears to contain a secret and was not stored")
        normalized_tags = sorted({tag.strip() for tag in tags or [] if tag.strip()})
        record = {
            "op": "store",
            "id": uuid.uuid4().hex,
            "content": content,
            "tags": normalized_tags,
            "createdAt": int(time.time()),
        }
        self._append(record)
        return record

    def search(self, query: str, limit: int = 5) -> list[dict[str, Any]]:
        query = query.strip().lower()
        if not query:
            raise ValueError("query must not be empty")
        if limit < 1 or limit > 50:
            raise ValueError("limit must be between 1 and 50")
        records = self._materialize()
        matches = [
            record
            for record in records.values()
            if query
            in (record["content"] + " " + " ".join(record.get("tags", []))).lower()
        ]
        matches.sort(key=lambda record: (record["createdAt"], record["id"]), reverse=True)
        return matches[:limit]

    def forget(self, memory_id: str) -> dict[str, Any]:
        memory_id = memory_id.strip()
        if not memory_id:
            raise ValueError("id must not be empty")
        if memory_id not in self._materialize():
            raise ValueError(f"memory {memory_id!r} was not found")
        event = {"op": "forget", "id": memory_id, "createdAt": int(time.time())}
        self._append(event)
        return event

    def _append(self, event: dict[str, Any]) -> None:
        self.path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        descriptor = os.open(
            self.path,
            os.O_APPEND | os.O_CREAT | os.O_WRONLY,
            0o600,
        )
        try:
            payload = json.dumps(event, separators=(",", ":"), ensure_ascii=False)
            os.write(descriptor, (payload + "\n").encode("utf-8"))
            os.fsync(descriptor)
        finally:
            os.close(descriptor)

    def _materialize(self) -> dict[str, dict[str, Any]]:
        if not self.path.exists():
            return {}
        records: dict[str, dict[str, Any]] = {}
        with self.path.open("r", encoding="utf-8") as source:
            for line_number, line in enumerate(source, start=1):
                try:
                    event = json.loads(line)
                except json.JSONDecodeError as error:
                    raise ValueError(
                        f"invalid JSONL at line {line_number}: {error.msg}"
                    ) from error
                operation = event.get("op")
                memory_id = event.get("id")
                if not isinstance(memory_id, str):
                    raise ValueError(f"missing memory id at line {line_number}")
                if operation == "store":
                    records[memory_id] = event
                elif operation == "forget":
                    records.pop(memory_id, None)
                else:
                    raise ValueError(f"unknown operation at line {line_number}")
        return records


TOOLS = [
    {
        "name": "memory_store",
        "description": "Store one durable local memory after checking for common secret formats.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "content": {"type": "string"},
                "tags": {"type": "array", "items": {"type": "string"}},
            },
            "required": ["content"],
            "additionalProperties": False,
        },
    },
    {
        "name": "memory_search",
        "description": "Search durable local memories by case-insensitive keyword.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1, "maximum": 50},
            },
            "required": ["query"],
            "additionalProperties": False,
        },
    },
    {
        "name": "memory_forget",
        "description": "Append a tombstone for one durable local memory.",
        "inputSchema": {
            "type": "object",
            "properties": {"id": {"type": "string"}},
            "required": ["id"],
            "additionalProperties": False,
        },
    },
]


def call_tool(store: MemoryStore, name: str, arguments: dict[str, Any]) -> Any:
    if name == "memory_store":
        return store.store(arguments.get("content", ""), arguments.get("tags"))
    if name == "memory_search":
        return store.search(arguments.get("query", ""), arguments.get("limit", 5))
    if name == "memory_forget":
        return store.forget(arguments.get("id", ""))
    raise ValueError(f"unknown tool {name!r}")


def result_text(value: Any, *, is_error: bool = False) -> dict[str, Any]:
    return {
        "content": [
            {
                "type": "text",
                "text": json.dumps(value, separators=(",", ":"), ensure_ascii=False)
                if not isinstance(value, str)
                else value,
            }
        ],
        "isError": is_error,
    }


def handle_request(store: MemoryStore, request: dict[str, Any]) -> dict[str, Any] | None:
    request_id = request.get("id")
    method = request.get("method")
    if request_id is None:
        return None
    if method == "initialize":
        requested = request.get("params", {}).get("protocolVersion", "2025-06-18")
        return {
            "jsonrpc": "2.0",
            "id": request_id,
            "result": {
                "protocolVersion": requested,
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "j-hermes-memory", "version": "0.1.0"},
            },
        }
    if method == "ping":
        return {"jsonrpc": "2.0", "id": request_id, "result": {}}
    if method == "tools/list":
        return {"jsonrpc": "2.0", "id": request_id, "result": {"tools": TOOLS}}
    if method == "tools/call":
        params = request.get("params", {})
        try:
            value = call_tool(store, params.get("name", ""), params.get("arguments", {}))
            result = result_text(value)
        except (OSError, TypeError, ValueError) as error:
            result = result_text(str(error), is_error=True)
        return {"jsonrpc": "2.0", "id": request_id, "result": result}
    return {
        "jsonrpc": "2.0",
        "id": request_id,
        "error": {"code": -32601, "message": f"method {method!r} not found"},
    }


def main() -> int:
    path = Path(os.environ.get("J_MEMORY_PATH", "~/.j/package-memory.jsonl"))
    store = MemoryStore(path)
    for line in sys.stdin:
        try:
            request = json.loads(line)
            response = handle_request(store, request)
        except (TypeError, ValueError, json.JSONDecodeError) as error:
            response = {
                "jsonrpc": "2.0",
                "id": None,
                "error": {"code": -32700, "message": str(error)},
            }
        if response is not None:
            sys.stdout.write(json.dumps(response, separators=(",", ":")) + "\n")
            sys.stdout.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
