"""Tests for capture.py and tools.py.

Run from the repository root:

    python3 -B -m unittest discover -s specs/047-rune-only-agent-mode/observations/codex
"""
import io
import json
import os
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from contextlib import redirect_stdout

import capture
import tools

EXEC_DESCRIPTION = (
    "Run JavaScript code to orchestrate/compose tool calls\n"
    "\n"
    "### `apply_patch`\n"
    "Edits files.\n"
    "\n"
    "### `exec_command`\n"
    "Runs a command in a PTY.\n"
)


def function(name, description=""):
    return {"type": "function", "name": name, "description": description}


def namespace(name, members):
    return {"type": "namespace", "name": name, "tools": members}


class WalkTest(unittest.TestCase):
    def test_flat_tools_keep_their_names(self):
        found = list(tools.walk([function("a"), function("b")]))
        self.assertEqual([name for name, _ in found], ["a", "b"])

    def test_namespace_members_get_the_namespace_as_prefix(self):
        tree = [namespace("functions", [function("exec"), namespace("inner", [function("x")])])]
        self.assertEqual([name for name, _ in tools.walk(tree)], ["functions.exec", "functions.inner.x"])

    def test_tool_without_a_name_is_named_after_its_type(self):
        self.assertEqual([name for name, _ in tools.walk([{"type": "web_search"}])], ["web_search"])

    def test_yields_the_tool_itself(self):
        tool = function("a", "text")
        self.assertIs(list(tools.walk([tool]))[0][1], tool)

    def test_namespace_without_members_yields_nothing(self):
        self.assertEqual(list(tools.walk([{"type": "namespace", "name": "empty"}])), [])


class RequestToolsTest(unittest.TestCase):
    def test_reads_the_tools_key(self):
        self.assertEqual(tools.request_tools({"tools": [function("a")]}), [function("a")])

    def test_reads_additional_tools_items_of_the_input(self):
        body = {
            "tools": None,
            "input": [
                {"type": "message", "tools": [function("ignored")]},
                {"type": "additional_tools", "tools": [function("a")]},
                {"type": "additional_tools"},
            ],
        }
        self.assertEqual(tools.request_tools(body), [function("a")])

    def test_joins_both_places(self):
        body = {"tools": [function("a")], "input": [{"type": "additional_tools", "tools": [function("b")]}]}
        self.assertEqual([t["name"] for t in tools.request_tools(body)], ["a", "b"])

    def test_empty_request_has_no_tools(self):
        self.assertEqual(tools.request_tools({}), [])


class ToolNamesTest(unittest.TestCase):
    def test_lists_the_tools_nested_in_a_namespaced_exec(self):
        body = {"tools": [namespace("functions", [function("exec", EXEC_DESCRIPTION), function("wait")])]}
        self.assertEqual(
            tools.tool_names(body),
            (["functions.exec", "functions.wait"], ["apply_patch", "exec_command"]),
        )

    def test_lists_the_tools_nested_in_a_top_level_exec(self):
        body = {"tools": [function("exec", EXEC_DESCRIPTION)]}
        self.assertEqual(tools.tool_names(body), (["exec"], ["apply_patch", "exec_command"]))

    def test_a_tool_whose_name_only_ends_in_exec_is_not_exec(self):
        body = {"tools": [function("remote_exec", EXEC_DESCRIPTION)]}
        self.assertEqual(tools.tool_names(body), (["remote_exec"], []))

    def test_no_exec_means_no_nested_tools(self):
        self.assertEqual(tools.tool_names({"tools": [function("a")]}), (["a"], []))

    def test_exec_without_a_description_has_no_nested_tools(self):
        self.assertEqual(tools.tool_names({"tools": [{"type": "custom", "name": "exec"}]}), (["exec"], []))


class ReportTest(unittest.TestCase):
    def test_prints_both_levels(self):
        body = {"tools": [function("exec", EXEC_DESCRIPTION), function("wait")]}
        self.assertEqual(
            tools.report(body),
            "top-level: exec, wait\nnested in exec: apply_patch, exec_command",
        )

    def test_says_when_nothing_is_nested(self):
        self.assertEqual(tools.report({"tools": [function("a")]}), "top-level: a\nnested in exec: (none listed)")


class ReadCaptureTest(unittest.TestCase):
    def test_reads_the_body_of_the_first_request(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "x.cap")
            with open(path, "wb") as f:
                f.write(b'{"path": "/v1/responses"}\n' + json.dumps({"tools": [function("a")]}).encode() + b"\n")
                f.write(b'{"path": "/v1/responses"}\n{"tools": []}\n')
            self.assertEqual(tools.read_capture(path), {"tools": [function("a")]})

    def test_main_prints_the_report(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "x.cap")
            with open(path, "wb") as f:
                f.write(b'{}\n{"tools": [{"type": "function", "name": "a"}]}\n')
            out = io.StringIO()
            with redirect_stdout(out):
                tools.main(path)
            self.assertEqual(out.getvalue(), "top-level: a\nnested in exec: (none listed)\n")


class CaptureTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.out = os.path.join(self.dir.name, "x.cap")
        self.server = capture.serve(0, self.out)
        self.addCleanup(self.server.server_close)
        thread = threading.Thread(target=self.server.serve_forever)
        thread.start()
        self.addCleanup(thread.join)
        self.addCleanup(self.server.shutdown)
        self.url = "http://127.0.0.1:%d" % self.server.server_address[1]

    def post(self, path, body, headers=None):
        request = urllib.request.Request(self.url + path, data=body, headers=headers or {}, method="POST")
        with self.assertRaises(urllib.error.HTTPError) as raised:
            urllib.request.urlopen(request)
        return raised.exception

    def test_listens_on_loopback_only(self):
        self.assertEqual(self.server.server_address[0], "127.0.0.1")

    def test_a_post_is_answered_with_an_error(self):
        error = self.post("/v1/responses", b'{"model": "m"}')
        self.assertEqual(error.code, 400)
        self.assertEqual(error.headers["content-type"], "application/json")
        self.assertEqual(json.loads(error.read()), {"error": {"message": "capture only"}})

    def test_a_post_is_written_as_a_line_about_it_and_its_body(self):
        self.post("/v1/responses", b'{"model": "m"}', {"content-encoding": "zstd"})
        with open(self.out, "rb") as f:
            lines = f.read().split(b"\n")
        self.assertEqual(json.loads(lines[0]), {"path": "/v1/responses", "encoding": "zstd"})
        self.assertEqual(lines[1], b'{"model": "m"}')
        self.assertEqual(lines[2], b"")

    def test_a_post_without_an_encoding_records_none(self):
        self.post("/v1/responses", b"{}")
        with open(self.out, "rb") as f:
            self.assertEqual(json.loads(f.readline()), {"path": "/v1/responses", "encoding": None})

    def test_a_second_post_is_appended(self):
        self.post("/a", b"1")
        self.post("/b", b"2")
        with open(self.out, "rb") as f:
            lines = f.read().split(b"\n")
        self.assertEqual([json.loads(lines[0])["path"], lines[1]], ["/a", b"1"])
        self.assertEqual([json.loads(lines[2])["path"], lines[3]], ["/b", b"2"])

    def test_tools_reads_what_capture_wrote(self):
        self.post("/v1/responses", json.dumps({"tools": [function("a")]}).encode())
        self.assertEqual(tools.read_capture(self.out), {"tools": [function("a")]})

    def test_a_get_is_not_found_and_writes_nothing(self):
        with self.assertRaises(urllib.error.HTTPError) as raised:
            urllib.request.urlopen(self.url + "/v1/models")
        self.assertEqual(raised.exception.code, 404)
        self.assertFalse(os.path.exists(self.out))


if __name__ == "__main__":
    unittest.main()
