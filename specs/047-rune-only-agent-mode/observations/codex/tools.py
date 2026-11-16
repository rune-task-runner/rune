"""Print the tool names in a captured Codex request: top-level tools, and the tools nested in exec."""
import json
import re
import sys


def walk(tools, prefix=""):
    for t in tools:
        kind = t.get("type")
        name = t.get("name") or kind
        if kind == "namespace":
            yield from walk(t.get("tools", []), prefix + name + ".")
        else:
            yield prefix + name, t


lines = open(sys.argv[1], "rb").read().split(b"\n")
body = json.loads(lines[1])
tools = list(body.get("tools") or [])
for item in body.get("input", []):
    if item.get("type") == "additional_tools":
        tools += item.get("tools", [])
top, nested = [], []
for name, t in walk(tools):
    top.append(name)
    if name == "exec" or name.endswith(".exec"):
        nested = re.findall(r"^### `([^`]+)`", t.get("description", ""), re.M)
print("top-level:", ", ".join(top))
print("nested in exec:", ", ".join(nested) if nested else "(none listed)")
