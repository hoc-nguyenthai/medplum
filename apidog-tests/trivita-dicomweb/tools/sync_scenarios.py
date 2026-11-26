#!/usr/bin/env python3
"""Create/update Apidog test scenarios from scenarios/<slug>.design.json and verify what was stored.

Usage:
  sync_scenarios.py <module-dir> --project <P> [--branch <name>] [--only a,b] [--verify-only]

For every scenarios/<slug>.design.json (or --only):
  1. no id in dryrun/ids.json → set folderId in <slug>.create.json, `cli-schema validate
     test-scenario-create`, `test-scenario create`, record the id (reruns never duplicate)
  2. `test-scenario get --with-case-detail` → scenarios/build/<slug>.before.json
  3. build the steps (build_scenario_update.build, keeping existing ids) → <slug>.update.json,
     `cli-schema validate test-scenario-update`, `test-scenario update --file`
  4. get again → scenarios/build/<slug>.after.json and compare with what was sent: step count,
     type, method/path, auth type, processor count, form field names. `update` reports success
     even when the server drops fields, so this check is the real result.
--verify-only skips 1 and 3 (no writes): compares the current server state with <slug>.update.json.

dryrun/ids.json must hold "folderId" (a test-scenario folder) before the first create.
Exit code 1 if any scenario fails or does not match.
"""
import argparse
import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import build_scenario_update as builder  # noqa: E402


def apidog(*args):
    """Run the CLI and return its JSON envelope (update notices go to stderr / are skipped)."""
    # stdout qua pipe bị CLI cắt ở 64 KiB (Node thoát trước khi xả hết) -> ghi vào tệp tạm
    import tempfile
    with tempfile.TemporaryFile("w+") as f:
        subprocess.run(["apidog", *args], stdout=f, stderr=subprocess.DEVNULL, text=True)
        f.seek(0)
        out = f.read()
    start = out.find("{")
    if start < 0:
        return {"success": False, "error": {"message": out.strip()[:300] or "no output"}}
    return json.loads(out[start:])


def err(res):
    return json.dumps(res.get("error") or res, ensure_ascii=False)[:300]


def validate(key, path):
    res = apidog("cli-schema", "validate", key, "--file", str(path))
    return bool((res.get("data") or {}).get("valid")), err(res)


def compare(sent, stored):
    """Differences between the steps we sent and the steps the server stored."""
    issues = []
    if len(sent) != len(stored):
        issues.append(f"steps sent {len(sent)} / stored {len(stored)}")
    for i, (a, b) in enumerate(zip(sent, stored), start=1):
        key = "customHttpRequest" if a["type"] == "customHttp" else "httpApiCase"
        ca, cb = a[key], b.get(key) or {}
        tag = f"step {i} ({a['name']})"
        if b.get("type") != a["type"]:
            issues.append(f"{tag}: type {b.get('type')}")
        if not cb.get("path") or not cb.get("method"):
            issues.append(f"{tag}: empty method/path")
        elif (ca["method"], ca["path"]) != (cb["method"], cb["path"]):
            issues.append(f"{tag}: {ca['method']} {ca['path']} → {cb['method']} {cb['path']}")
        if (ca.get("auth") or {}).get("type") != (cb.get("auth") or {}).get("type"):
            issues.append(f"{tag}: auth {(ca.get('auth') or {}).get('type')} → {(cb.get('auth') or {}).get('type')}")
        if len(ca.get("postProcessors") or []) != len(cb.get("postProcessors") or []):
            issues.append(f"{tag}: processors {len(ca.get('postProcessors') or [])} → {len(cb.get('postProcessors') or [])}")
        sent_form = [p["name"] for p in (ca.get("requestBody") or {}).get("parameters") or []]
        got_form = [p.get("name") for p in (cb.get("requestBody") or {}).get("parameters") or []]
        if sent_form != got_form:
            issues.append(f"{tag}: form {sent_form} → {got_form}")
    return issues


def warnings(stored, design_steps):
    """Bound steps with built-in response validation (responseId ≠ 0) that expect a non-2xx
    status: the validation fails them and skips the rest of the iteration."""
    out = []
    for i, (b, d) in enumerate(zip(stored, design_steps), start=1):
        case = b.get("httpApiCase") or {}
        if (b.get("type") == "http" and case.get("responseId") not in (None, 0, "0")
                and builder.may_expect_non_2xx(d)):
            out.append(f"step {i} ({b.get('name')}): built-in response validation active "
                       f"(responseId {case['responseId']}) — mark \"custom\": true if a row expects another status")
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("module_dir")
    ap.add_argument("--project", required=True, type=int)
    ap.add_argument("--branch")
    ap.add_argument("--only", help="comma-separated slugs")
    ap.add_argument("--verify-only", action="store_true")
    a = ap.parse_args()

    root = Path(a.module_dir)
    ids_path = root / "dryrun" / "ids.json"
    ids = json.loads(ids_path.read_text(encoding="utf-8")) if ids_path.exists() else {}
    ids.setdefault("scenarios", {})
    branch = ["--branch", a.branch] if a.branch else []
    slugs = a.only.split(",") if a.only else sorted(p.name[:-len(".design.json")] for p in (root / "scenarios").glob("*.design.json"))
    (root / "scenarios" / "build").mkdir(parents=True, exist_ok=True)

    failed = False
    for slug in slugs:
        sid = ids["scenarios"].get(slug)
        if sid is None:
            if a.verify_only:
                print(f"✗ {slug}: no scenario id in dryrun/ids.json")
                failed = True
                continue
            if not ids.get("folderId"):
                sys.exit("dryrun/ids.json has no folderId (create a test-scenario folder first)")
            create = root / "scenarios" / f"{slug}.create.json"
            payload = json.loads(create.read_text(encoding="utf-8"))
            payload["folderId"] = ids["folderId"]
            create.write_text(json.dumps(payload, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
            ok, msg = validate("test-scenario-create", create)
            if not ok:
                print(f"✗ {slug}: create payload invalid {msg}")
                failed = True
                continue
            res = apidog("test-scenario", "create", "--project", str(a.project), "--file", str(create), *branch)
            sid = (res.get("data") or {}).get("id")
            if not sid:
                print(f"✗ {slug}: create failed {err(res)}")
                failed = True
                continue
            ids["scenarios"][slug] = sid
            ids_path.write_text(json.dumps(ids, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")

        get = ["test-scenario", "get", str(sid), "--project", str(a.project), "--with-case-detail", *branch]
        before = apidog(*get)
        (root / "scenarios" / "build" / f"{slug}.before.json").write_text(json.dumps(before, ensure_ascii=False, indent=1), encoding="utf-8")
        upd = root / "scenarios" / f"{slug}.update.json"

        if not a.verify_only:
            try:
                steps = builder.build(root, slug, a.project, (before.get("data") or {}).get("steps") or [])
            except SystemExit as error:  # builder reports design problems this way
                print(f"✗ {slug}: build failed: {error}")
                failed = True
                continue
            upd.write_text(json.dumps({"steps": steps}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
            ok, msg = validate("test-scenario-update", upd)
            if not ok:
                print(f"✗ {slug}: update payload invalid {msg}")
                failed = True
                continue
            res = apidog("test-scenario", "update", str(sid), "--project", str(a.project), "--file", str(upd), *branch)
            if not res.get("success"):
                print(f"✗ {slug}: update failed {err(res)}")
                failed = True
                continue
            after = apidog(*get)
        else:
            after = before
        (root / "scenarios" / "build" / f"{slug}.after.json").write_text(json.dumps(after, ensure_ascii=False, indent=1), encoding="utf-8")

        sent = json.loads(upd.read_text(encoding="utf-8"))["steps"]
        stored = (after.get("data") or {}).get("steps") or []
        issues = compare(sent, stored)
        mark = "✓" if not issues else "✗"
        print(f"{mark} {slug} ({sid}): {len(stored)} step(s)")
        for msg in issues:
            print(f"    MISMATCH {msg}")
        design = json.loads((root / "scenarios" / f"{slug}.design.json").read_text(encoding="utf-8"))
        for msg in warnings(stored, design["steps"]):
            print(f"    WARNING  {msg}")
        failed |= bool(issues)
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
