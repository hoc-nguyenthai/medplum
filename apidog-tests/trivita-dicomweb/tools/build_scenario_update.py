#!/usr/bin/env python3
"""Build a test-scenario-update payload from <slug>.design.json.

Usage: build_scenario_update.py <module-dir> <slug> <projectId> [expanded.json]

Reads  <module-dir>/scenarios/<slug>.design.json
       <module-dir>/spec/endpoint-details/<endpointId>.json  (apidog endpoint get ... | .data)
       [expanded.json]  optional `test-scenario get --with-case-detail` output; existing steps
                        are matched by position so their relatedId / httpApiCase.id are kept
Writes <module-dir>/scenarios/<slug>.update.json

Design step format:
  {"name", "endpointId", "method", "path" (values in place of {params}), "query": {k: v},
   "headers": {k: v}, "body": "<raw JSON string>" | "form": {k: v} (x-www-form-urlencoded),
   "auth": "Bearer {{var}}" | "none" (optional; default = the endpoint's own auth),
   "custom": true (optional; force customHttp — automatic for form bodies and for steps whose
                   status assertion may be non-2xx, see may_expect_non_2xx),
   "postProcessors": [{"type": "extractor", "data": {...}} | {"type": "customScript", "data": {"script": "..."}}],
   "assertions": ["status == X", "$.p == X", "$.p != X", "$.p exists", "$.p not exists", "$.p is array"]}
"""
import json
import re
import sys
from pathlib import Path

ASSERT_RE = re.compile(r"^(status|\$[^\s]*)\s+(==|!=|is array|not exists|exists)\s*(.*)$")


def assertion(expr):
    m = ASSERT_RE.match(expr.strip())
    if not m:
        raise SystemExit(f"cannot parse assertion: {expr!r}")
    left, op, right = m.groups()
    if op == "is array":  # simple dotted/indexed JSONPath only, e.g. $.data or $.data.items
        js = "pm.response.json()" + left[1:]
        return {"type": "customScript", "enable": True,
                "data": f"pm.test({json.dumps(expr)}, () => pm.expect(Array.isArray({js})).to.eql(true));"}
    comparison = {"==": "equal", "!=": "notEqual", "exists": "exists", "not exists": "notExist"}[op]
    if left == "status":
        return {"type": "assertion", "enable": True,
                "data": {"name": expr, "subject": "httpCode", "comparison": comparison, "path": "", "value": right}}
    return {"type": "assertion", "enable": True,
            "data": {"name": expr, "subject": "responseJson", "comparison": comparison, "path": left,
                     "value": right, "extractSettings": {"expression": left}}}


def processor(p):
    if p["type"] == "customScript" and isinstance(p.get("data"), dict):
        return {"type": "customScript", "enable": True, "data": p["data"]["script"]}
    if p["type"] == "extractor":
        return {"type": "extractor", "enable": True, "data": dict({"variableType": "local"}, **p["data"])}
    return p


def bearer_scheme_source(root, auth):
    """Find a cached endpoint whose security scheme sends `auth` ("Bearer {{var}}")."""
    token = auth.split(" ", 1)[-1]
    for f in sorted((root / "spec" / "endpoint-details").glob("*.json")):
        ep = json.loads(f.read_text(encoding="utf-8"))
        configs = ((ep.get("securityScheme") or {}).get("use") or {}).get("configs") or {}
        if any((c.get("authConfigs") or {}).get("x-apidog", {}).get("token") == token for c in configs.values()):
            return ep
    return None


def resolve_auth(root, ep, wanted):
    """Auth fields for the step case.

    wanted: None (use the endpoint's own config), "none" (send no credentials) or
    "Bearer {{var}}". For a bearer token, prefer the endpoint's scheme when it already sends
    that variable, then a sibling endpoint's scheme (verified to persist), then an explicit
    ``bearer`` auth (for schemes such as OAuth2 that carry no token variable).
    """
    own = {"auth": ep["auth"], "securityScheme": ep["securityScheme"]} \
        if (ep.get("auth") or {}).get("type") == "securityscheme" else {}
    if wanted is None:
        return own
    if wanted == "none":
        return {"auth": {"type": "noauth"}}
    if not wanted.startswith("Bearer "):
        raise SystemExit(f"unsupported auth: {wanted!r}")
    token = wanted.split(" ", 1)[1]
    if own and token in json.dumps(own["securityScheme"]):
        return own
    sibling = bearer_scheme_source(root, wanted)
    if sibling is not None:
        return {"auth": sibling["auth"], "securityScheme": sibling["securityScheme"]}
    return {"auth": {"type": "bearer", "bearer": {"token": token}}}


def may_expect_non_2xx(design_step):
    """True when a status assertion may not be 2xx ({{var}} or a 3xx-5xx literal).

    Such steps go as customHttp: a step bound to the API gets built-in response validation pinned
    by the server to the endpoint's first response (responseId 0 is overwritten); a non-matching
    status fails it and skips the rest of the iteration even with --on-error continue.
    """
    for a in design_step.get("assertions", []):
        if a.startswith("status =="):
            value = a.split("==", 1)[1].strip()
            if "{{" in value or not value.startswith("2"):
                return True
    return False


def api_key_location(root):
    """(name, in) of the module's single apiKey security scheme in spec/openapi.json, if any."""
    spec = root / "spec" / "openapi.json"
    if not spec.exists():
        return None
    schemes = json.loads(spec.read_text(encoding="utf-8")).get("components", {}).get("securitySchemes", {})
    keys = [(v.get("name"), v.get("in")) for v in schemes.values() if v.get("type") == "apiKey"]
    return keys[0] if len(keys) == 1 else None


def custom_request(case, project, api_key=None):
    """customHttp payload from a built case; the security scheme becomes an explicit auth.

    The relative path resolves against the run environment's base URL.
    """
    auth = case.get("auth") or {}
    if auth.get("type") == "securityscheme":
        configs = ((case.get("securityScheme") or {}).get("use") or {}).get("configs") or {}
        cfg = next(((c.get("authConfigs") or {}).get("x-apidog") or {} for c in configs.values()), {})
        if "username" in cfg:
            auth = {"type": "basic", "basic": {"username": cfg["username"], "password": cfg.get("password", "")}}
        elif "token" in cfg:
            auth = {"type": "bearer", "bearer": {"token": cfg["token"]}}
        elif "value" in cfg and api_key:
            auth = {"type": "apikey", "apikey": {"key": api_key[0], "value": cfg["value"], "in": api_key[1]}}
        else:
            raise SystemExit(f"cannot convert security scheme of {case['path']} for a customHttp step")
    # Inline path params: a customHttp request is a plain URL, not an endpoint template.
    path = case["path"]
    for p in case["parameters"]["path"]:
        path = path.replace("{" + p["name"] + "}", p["value"])
    # customHttp: Apidog lưu parameters.query nhưng lúc chạy KHÔNG gửi (đã kiểm: report url.query rỗng) -> nhúng query vào chính path
    from urllib.parse import quote
    q = [x for x in case["parameters"].get("query", []) if x.get("enable", True)]
    if q:
        path += ("&" if "?" in path else "?") + "&".join(
            f'{quote(x["name"], safe="{}")}={quote(str(x["value"]), safe="{}")}' for x in q)
    parameters = dict(case["parameters"], path=[], query=[])
    return {"id": 0, "name": case["name"], "path": path, "method": case["method"], "folderId": 0,
            "parameters": parameters, "requestBody": case["requestBody"], "preProcessors": [],
            "postProcessors": case["postProcessors"], "auth": auth, "advancedSettings": {},
            "commonParameters": {}, "projectId": project}


def request_body(s):
    if s.get("form") is not None:
        return {"type": "x-www-form-urlencoded",
                "parameters": [{"name": k, "value": v, "enable": True, "type": "string"} for k, v in s["form"].items()]}
    if s.get("body") is not None:
        # PATCH cục bộ so với bản của skill: "bodyType": "raw" gửi body nguyên văn (multipart/related của STOW-RS), Content-Type lấy từ header của step
        return {"type": s.get("bodyType", "json"), "data": s["body"]}
    return {"type": "none"}


def path_params(template, actual):
    """Align '/a/{id}/b' with '/a/{{x}}/b' → [{'name': 'id', 'value': '{{x}}'}]."""
    t, a = template.strip("/").split("/"), actual.strip("/").split("/")
    if len(t) != len(a):
        raise SystemExit(f"path mismatch: {template} vs {actual}")
    out = []
    for ts, as_ in zip(t, a):
        if ts.startswith("{") and ts.endswith("}"):
            out.append({"name": ts[1:-1], "value": as_, "enable": True})
        elif ts != as_:
            raise SystemExit(f"path mismatch: {template} vs {actual}")
    return out


def build(root, slug, project, existing=()):
    """The `steps` array for <slug>; `existing` = steps from `get --with-case-detail` (keeps ids)."""
    design = json.loads((root / "scenarios" / f"{slug}.design.json").read_text(encoding="utf-8"))
    existing = list(existing)
    steps = []
    for i, s in enumerate(design["steps"], start=1):
        ep = json.loads((root / "spec" / "endpoint-details" / f"{s['endpointId']}.json").read_text(encoding="utf-8"))
        headers = [{"name": k, "value": v, "enable": True} for k, v in (s.get("headers") or {}).items()]
        auth = resolve_auth(root, ep, s.get("auth"))
        if auth:  # the auth config sends the credential header; Apidog drops undeclared ones anyway
            headers = [h for h in headers if h["name"].lower() not in ("authorization", "x-api-key")]
        case = {
            "type": "DEBUG_CASE",
            "name": s["name"],
            "path": ep["path"],
            "method": ep["method"].lower(),
            "apiDetailId": s["endpointId"],
            "projectId": project,
            "parameters": {
                "path": path_params(ep["path"], s["path"]),
                "query": [{"name": k, "value": v, "enable": True} for k, v in (s.get("query") or {}).items()],
                "header": headers,
                "cookie": [],
            },
            "requestBody": request_body(s),
            "postProcessors": [assertion(a) for a in s.get("assertions", [])]
                              + [processor(p) for p in s.get("postProcessors", [])],
        }
        case.update(auth)
        old = existing[i - 1] if i <= len(existing) else {}
        declared_q = {q.get("name") for q in ((ep.get("parameters") or {}).get("query") or [])}
        if s.get("form") is not None or s.get("body") is not None or s.get("custom") or may_expect_non_2xx(s) or (declared_q - set((s.get("query") or {}).keys())):
            # A step bound to the API keeps only the form fields the endpoint declares (others are
            # dropped, declared-but-unused ones are sent empty), so form requests go as customHttp.
            # "custom": true also avoids the built-in response validation, which the server pins
            # to the endpoint's first response (responseId 0 is overwritten) and which stops the
            # iteration when a data-driven row expects another status.
            step = {"id": f"step-{i}", "number": i, "name": s["name"], "type": "customHttp",
                    "customHttpRequest": custom_request(case, project, api_key_location(root))}
            if old.get("type") == "customHttp":
                # Without the stored request id the server keeps the old request content silently.
                step["relatedId"] = old.get("relatedId")
                step["customHttpRequest"]["id"] = (old.get("customHttpRequest") or {}).get("id") or 0
        else:
            step = {"id": f"step-{i}", "number": i, "name": s["name"], "type": "http",
                    "bindType": "API", "syncMode": "MANUAL", "bindId": s["endpointId"], "httpApiCase": case}
            if old.get("bindId") == s["endpointId"]:
                step["relatedId"] = old.get("relatedId")
                if (old.get("httpApiCase") or {}).get("id"):
                    case["id"] = old["httpApiCase"]["id"]
        steps.append(step)
    return steps


def main():
    if len(sys.argv) not in (4, 5):
        sys.exit(__doc__)
    root, slug, project = Path(sys.argv[1]), sys.argv[2], int(sys.argv[3])
    existing = []
    if len(sys.argv) == 5:
        exp = json.loads(Path(sys.argv[4]).read_text(encoding="utf-8"))
        existing = (exp.get("data") or exp).get("steps") or []
    steps = build(root, slug, project, existing)
    out = root / "scenarios" / f"{slug}.update.json"
    out.write_text(json.dumps({"steps": steps}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    print(f"{out}: {len(steps)} step(s)")


if __name__ == "__main__":
    main()
