"""Compare version-matched Codex schema exports; Python 3 standard library only.

Usage: python scripts/compare-schemas.py OLD_EXPORT NEW_EXPORT
The JSON report is written to stdout. Differences are data, not a failing exit.
Compare stable to stable or experimental to experimental, never mix export modes.
"""
import argparse
import json
from pathlib import Path


def load(root):
    files = {}
    definitions = {}
    for path in sorted(root.rglob("*.json")):
        if path.name == "manifest.json":
            continue
        data = json.loads(path.read_text(encoding="utf-8"))
        if "$schema" not in data:
            continue
        name = path.relative_to(root).as_posix()
        files[name] = {k: v for k, v in data.items() if k != "definitions"}
        # Aggregates contain inline types that have no standalone schema file.
        if path.name in ("ClientRequest.json", "ServerRequest.json", "ServerNotification.json"):
            definitions.update(data.get("definitions", {}))
    if not files:
        raise ValueError(f"No JSON schemas in {root}")
    return files, definitions


def methods(files, name):
    result = set()
    for variant in files.get(name, {}).get("oneOf", []):
        method = variant.get("properties", {}).get("method", {})
        result.update(method.get("enum", []))
        if "const" in method:
            result.add(method["const"])
    return result


def changes(old, new, path=""):
    if isinstance(old, dict) and isinstance(new, dict):
        ignored = {"description", "title", "$schema"}
        for key in sorted((old.keys() | new.keys()) - ignored):
            child = f"{path}/{key}"
            if key not in old:
                yield {"kind": "field_added" if path.endswith("/properties") else "added", "path": child, "new": new[key]}
            elif key not in new:
                yield {"kind": "field_removed" if path.endswith("/properties") else "removed", "path": child, "old": old[key]}
            elif key in ("enum", "required"):
                a, b = set(old[key]), set(new[key])
                if a != b:
                    yield {"kind": key + "_changed", "path": child, "added": sorted(b-a), "removed": sorted(a-b)}
            else:
                yield from changes(old[key], new[key], child)
    elif isinstance(old, list) and isinstance(new, list):
        # Match tagged unions by their wire discriminator so adding a variant
        # doesn't turn every following variant into a spurious field change.
        def tag(value):
            if not isinstance(value, dict):
                return None
            for key in ("type", "method"):
                enum = value.get("properties", {}).get(key, {}).get("enum", [])
                if len(enum) == 1:
                    return str(enum[0])
            enum = value.get("enum", [])
            return str(enum[0]) if len(enum) == 1 else None
        a, b = [tag(v) for v in old], [tag(v) for v in new]
        if all(v is not None for v in a+b) and len(set(a)) == len(a) and len(set(b)) == len(b):
            yield from changes(dict(zip(a, old)), dict(zip(b, new)), path)
        else:
            for index in range(max(len(old), len(new))):
                if index >= len(old):
                    yield {"kind": "added", "path": f"{path}/{index}", "new": new[index]}
                elif index >= len(new):
                    yield {"kind": "removed", "path": f"{path}/{index}", "old": old[index]}
                else:
                    yield from changes(old[index], new[index], f"{path}/{index}")
    elif old != new:
        yield {"kind": "changed", "path": path, "old": old, "new": new}


def compare(old_root, new_root):
    old, old_defs = load(old_root)
    new, new_defs = load(new_root)
    aggregates = ("ClientRequest.json", "ServerRequest.json", "ServerNotification.json", "ClientNotification.json")
    report = {"methods": {}, "filesAdded": sorted(new.keys()-old.keys()), "filesRemoved": sorted(old.keys()-new.keys()), "changes": []}
    for name in aggregates:
        a, b = methods(old, name), methods(new, name)
        report["methods"][name] = {"oldCount": len(a), "newCount": len(b), "added": sorted(b-a), "removed": sorted(a-b)}
    for name in sorted(old.keys() & new.keys()):
        if name in aggregates or name.startswith("codex_app_server_protocol"):
            continue
        report["changes"].extend(changes(old[name], new[name], "files/"+name))
    report["changes"].extend(changes(old_defs, new_defs, "definitions"))
    return report


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("old", type=Path)
    parser.add_argument("new", type=Path)
    args = parser.parse_args()
    print(json.dumps(compare(args.old, args.new), ensure_ascii=False, indent=2))
