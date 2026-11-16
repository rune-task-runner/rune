"""Print the tool names in a captured Codex request: top-level tools, and the tools nested in exec.

    python3 -I tools.py FILE
"""
import json
import re
import sys


def walk(tools, prefix=""):
    """Yield (name, tool) for every tool, with the names of its namespaces in front."""
    for t in tools:
        kind = t.get("type")
        name = t.get("name") or kind
        if kind == "namespace":
            yield from walk(t.get("tools", []), prefix + name + ".")
        else:
            yield prefix + name, t


def request_tools(body):
    """Return the tools of a request: its tools key, and the additional_tools items of its input."""
    tools = list(body.get("tools") or [])
    for item in body.get("input", []):
        if item.get("type") == "additional_tools":
            tools += item.get("tools", [])
    return tools


def tool_names(body):
    """Return the top-level tool names, and the names of the tools that exec describes."""
    top, nested = [], []
    for name, t in walk(request_tools(body)):
        top.append(name)
        if name == "exec" or name.endswith(".exec"):
            nested = re.findall(r"^### `([^`]+)`", t.get("description", ""), re.M)
    return top, nested


def report(body):
    top, nested = tool_names(body)
    return "top-level: %s\nnested in exec: %s" % (", ".join(top), ", ".join(nested) if nested else "(none listed)")


def read_capture(path):
    """Return the body of the first request in a file that capture.py wrote."""
    with open(path, "rb") as f:
        return json.loads(f.read().split(b"\n")[1])


def main(path):
    print(report(read_capture(path)))


if __name__ == "__main__":
    main(sys.argv[1])
