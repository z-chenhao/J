import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from server import MemoryStore


class MemoryStoreTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.path = Path(self.temporary.name) / "memory.jsonl"
        self.store = MemoryStore(self.path)

    def test_store_search_and_forget_are_append_only(self):
        created = self.store.store("J packages use MCP and skills.", ["J", "package"])
        self.assertEqual(
            self.store.search("mcp"),
            [created],
        )
        forgotten = self.store.forget(created["id"])
        self.assertEqual(forgotten["op"], "forget")
        self.assertEqual(self.store.search("mcp"), [])
        self.assertEqual(len(self.path.read_text(encoding="utf-8").splitlines()), 2)
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)

    def test_rejects_likely_secrets(self):
        with self.assertRaisesRegex(ValueError, "secret"):
            self.store.store("sk_" + "a" * 30)
        self.assertFalse(self.path.exists())

    def test_stdio_mcp_round_trip(self):
        environment = os.environ.copy()
        environment["J_MEMORY_PATH"] = str(self.path)
        process = subprocess.Popen(
            [sys.executable, "server.py"],
            cwd=Path(__file__).parent,
            env=environment,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.addCleanup(process.kill)
        requests = [
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {"protocolVersion": "2025-06-18"},
            },
            {"jsonrpc": "2.0", "method": "notifications/initialized"},
            {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}},
            {
                "jsonrpc": "2.0",
                "id": 3,
                "method": "tools/call",
                "params": {
                    "name": "memory_store",
                    "arguments": {"content": "remember package protocol"},
                },
            },
        ]
        assert process.stdin is not None
        assert process.stdout is not None
        for request in requests:
            process.stdin.write(json.dumps(request) + "\n")
        process.stdin.flush()
        responses = [json.loads(process.stdout.readline()) for _ in range(3)]
        self.assertEqual(responses[0]["result"]["protocolVersion"], "2025-06-18")
        self.assertEqual(
            [tool["name"] for tool in responses[1]["result"]["tools"]],
            ["memory_store", "memory_search", "memory_forget"],
        )
        self.assertFalse(responses[2]["result"]["isError"])
        process.stdin.close()
        self.assertEqual(process.wait(timeout=5), 0)
        process.stdout.close()
        assert process.stderr is not None
        process.stderr.close()


if __name__ == "__main__":
    unittest.main()
