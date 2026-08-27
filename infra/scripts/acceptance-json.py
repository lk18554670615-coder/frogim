#!/usr/bin/env python3
"""Small, dependency-free JSON helper for acceptance-local.sh.

It intentionally implements only the jq forms used by that script. A real jq
binary remains preferred when present.
"""

from __future__ import annotations

import json
import os
import re
import sys
from pathlib import Path
from typing import Any


def configure_utf8_streams() -> None:
    """Keep native Windows Python byte-compatible with Git Bash.

    MSYS passes command output through as bytes.  A native Python process uses
    the active Windows code page by default, which makes a valid Chinese JSON
    value compare unequal to the UTF-8 shell literal that produced it.
    """

    for stream in (sys.stdout, sys.stderr):
        reconfigure = getattr(stream, "reconfigure", None)
        if reconfigure is not None:
            reconfigure(encoding="utf-8", errors="strict")


def normalize_windows_argument(value: str) -> str:
    """Repair the GBK-as-Latin-1 argv form produced by MSYS on Windows.

    On a Chinese Windows host, an MSYS UTF-8 argument can reach a native
    process as characters whose code points are the original GBK bytes (for
    example ``验收`` becomes ``ÑéÊÕ``).  Only repair an argument when the
    original contains no CJK text and a reversible Latin-1 -> GBK conversion
    introduces CJK characters, so ordinary ASCII and genuine Unicode remain
    untouched.
    """

    if os.name != "nt" or any("\u3400" <= char <= "\u9fff" for char in value):
        return value
    if not any(0x80 <= ord(char) <= 0xFF for char in value):
        return value
    try:
        candidate = value.encode("latin-1").decode("gbk")
    except (UnicodeEncodeError, UnicodeDecodeError):
        return value
    if any("\u3400" <= char <= "\u9fff" for char in candidate):
        return candidate
    return value


def fail(message: str) -> "NoReturn":
    print(message, file=sys.stderr)
    raise SystemExit(2)


def parse_arguments(arguments: list[str]) -> tuple[bool, bool, dict[str, Any], str, str | None]:
    null_input = False
    exit_status = False
    variables: dict[str, Any] = {}
    position = 0
    while position < len(arguments):
        value = arguments[position]
        if value.startswith("-") and not value.startswith("--"):
            null_input = null_input or "n" in value
            exit_status = exit_status or "e" in value
            position += 1
            continue
        if value in {"--arg", "--argjson"}:
            if position + 2 >= len(arguments):
                fail(f"{value} requires a name and value")
            name, raw = arguments[position + 1 : position + 3]
            if value == "--argjson":
                try:
                    variables[name] = json.loads(raw)
                except json.JSONDecodeError as error:
                    fail(f"invalid --argjson {name}: {error}")
            else:
                variables[name] = raw
            position += 3
            continue
        break
    if position >= len(arguments):
        fail("missing expression")
    expression = arguments[position]
    file_name = arguments[position + 1] if position + 1 < len(arguments) else None
    if position + 2 < len(arguments):
        fail("unexpected trailing arguments")
    return null_input, exit_status, variables, expression, file_name


def build_json(expression: str, variables: dict[str, Any]) -> Any:
    def variable(match: re.Match[str]) -> str:
        name = match.group(1)
        if name not in variables:
            fail(f"unknown variable ${name}")
        return json.dumps(variables[name], ensure_ascii=True, separators=(",", ":"))

    rendered = re.sub(r"\$([A-Za-z_][A-Za-z0-9_]*)", variable, expression)
    rendered = re.sub(
        r"([\{,])\s*([A-Za-z_][A-Za-z0-9_]*)\s*:",
        lambda match: f'{match.group(1)}"{match.group(2)}":',
        rendered,
    )
    try:
        return json.loads(rendered)
    except json.JSONDecodeError as error:
        fail(f"unsupported JSON construction expression: {error}: {expression}")


def path_value(value: Any, expression: str) -> Any:
    if expression == ".":
        return value
    if not expression.startswith("."):
        fail(f"unsupported path: {expression}")
    current = value
    for name, index in re.findall(r"\.([A-Za-z_][A-Za-z0-9_]*)(?:\[([0-9]+)\])?", expression):
        if not isinstance(current, dict) or name not in current:
            raise KeyError(expression)
        current = current[name]
        if index:
            if not isinstance(current, list):
                raise KeyError(expression)
            current = current[int(index)]
    return current


def selected_items(data: Any, source_path: str, condition: str, variables: dict[str, Any]) -> list[Any]:
    source = path_value(data, source_path)
    if not isinstance(source, list):
        fail(f"selection source is not an array: {source_path}")
    condition_match = re.fullmatch(
        r"(\.[A-Za-z_][A-Za-z0-9_.]*)==\$([A-Za-z_][A-Za-z0-9_]*)",
        condition.replace(" ", ""),
    )
    if not condition_match:
        fail(f"unsupported select condition: {condition}")
    item_path, variable_name = condition_match.groups()
    if variable_name not in variables:
        fail(f"unknown select variable ${variable_name}")
    expected = variables[variable_name]
    selected: list[Any] = []
    for item in source:
        try:
            if path_value(item, item_path) == expected:
                selected.append(item)
        except (KeyError, IndexError, TypeError):
            continue
    return selected


def query_json(data: Any, expression: str, variables: dict[str, Any]) -> Any:
    expression = expression.strip()
    array_select = re.fullmatch(
        r"\[(\.[A-Za-z_][A-Za-z0-9_.]*)\[\]\s*\|\s*select\((.+)\)\]\s*\|\s*length",
        expression,
    )
    if array_select:
        return len(selected_items(data, array_select.group(1), array_select.group(2), variables))

    root_array_select = re.fullmatch(
        r"\[\.\[\]\s*\|\s*select\(\.==\$([A-Za-z_][A-Za-z0-9_]*)\)\]\s*\|\s*length",
        expression,
    )
    if root_array_select:
        if not isinstance(data, list):
            fail("root selection source is not an array")
        variable_name = root_array_select.group(1)
        if variable_name not in variables:
            fail(f"unknown select variable ${variable_name}")
        return sum(1 for item in data if item == variables[variable_name])

    item_select = re.fullmatch(
        r"(\.[A-Za-z_][A-Za-z0-9_.]*)\[\]\s*\|\s*select\((.+)\)\s*\|\s*(\.[A-Za-z_][A-Za-z0-9_.]*)",
        expression,
    )
    if item_select:
        selected = selected_items(data, item_select.group(1), item_select.group(2), variables)
        if not selected:
            raise KeyError(expression)
        return path_value(selected[0], item_select.group(3))

    startswith = re.fullmatch(r"(.+?)\s*\|\s*startswith\((.+)\)", expression)
    if startswith:
        value = path_value(data, startswith.group(1).strip())
        prefix = json.loads(startswith.group(2))
        return isinstance(value, str) and value.startswith(prefix)

    length = re.fullmatch(r"(.+?)\s*\|\s*length", expression)
    if length:
        return len(path_value(data, length.group(1).strip()))

    return path_value(data, expression)


def print_value(value: Any) -> None:
    if isinstance(value, str):
        print(value)
    elif value is True:
        print("true")
    elif value is False:
        print("false")
    elif value is None:
        print("null")
    else:
        # Keep the jq fallback byte-safe when a Windows-native Python process
        # is called from Git Bash. JSON escapes preserve the exact Unicode
        # value without depending on the active console code page.
        print(json.dumps(value, ensure_ascii=True, separators=(",", ":")))


def main() -> None:
    configure_utf8_streams()
    arguments = [normalize_windows_argument(value) for value in sys.argv[1:]]
    null_input, exit_status, variables, expression, file_name = parse_arguments(arguments)
    try:
        if null_input:
            result = build_json(expression, variables)
        else:
            if file_name is None:
                fail("query mode requires an input file")
            result = query_json(json.loads(Path(file_name).read_text(encoding="utf-8")), expression, variables)
    except (KeyError, IndexError, TypeError, json.JSONDecodeError) as error:
        print(f"JSON query failed: {expression}: {error}", file=sys.stderr)
        raise SystemExit(1) from error
    print_value(result)
    if exit_status and (result is None or result is False):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
