#!/usr/bin/env python3
"""Gom file test data theo module để import tay vào Apidog: apidog-tests/_import/<module>/NN-<slug>.(csv|json) + INDEX.md + <module>.zip.

Chạy: python3 -I apidog-tests/build_import_bundle.py   (đọc dryrun/ids.json, data/, scenarios/<slug>.create.json của từng module)
NN = thứ tự theo tên scenario (không nhất thiết trùng thứ tự hiển thị trong app; tìm theo tên hoặc ID ở INDEX.md). Dữ liệu sinh lại từ generate.py: chạy lại script này sau khi đổi dữ liệu.
"""
import csv, io, json, shutil, zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent
OUT = ROOT / "_import"
MODULES = [("trivita-auth", "Trivita Auth"), ("trivita-fhir-server", "Trivita FHIR Server"), ("trivita-dicomweb", "Trivita DICOMweb"),
           ("trivita-ws-gateway", "Trivita WS Gateway"), ("trivita-full-flow", "Trivita Full Flow")]


def bulk_text(src):
    """Nội dung dán vào Bulk Edit: dòng đầu 'Dataset Name,<cột>...', mỗi dòng bắt đầu bằng case_id. None nếu có ô nhiều dòng."""
    if src.suffix == ".json":
        data = json.loads(src.read_text(encoding="utf-8"))
        cols = list(data[0])
        table = [cols] + [[r.get(c, "") for c in cols] for r in data]
    else:
        table = list(csv.reader(io.StringIO(src.read_bytes().decode("utf-8-sig"), newline="")))
    if any(("\n" in c or "\r" in c) for row in table for c in row):
        return None
    buf = io.StringIO()
    w = csv.writer(buf, lineterminator="\n")
    w.writerow(["Dataset Name"] + table[0])
    for r in table[1:]:
        w.writerow([r[0]] + r)
    return buf.getvalue().rstrip("\n") + "\n"


def rows(p):
    if p.suffix == ".json":
        return len(json.loads(p.read_text(encoding="utf-8")))
    return max(0, len(p.read_text(encoding="utf-8-sig").strip().splitlines()) - 1)


shutil.rmtree(OUT, ignore_errors=True)
summary = []
for slug_dir, title in MODULES:
    m = ROOT / slug_dir
    ids = json.loads((m / "dryrun" / "ids.json").read_text(encoding="utf-8"))
    out = OUT / slug_dir
    out.mkdir(parents=True)
    items = []
    for slug, sid in ids["scenarios"].items():
        src = next((m / "data" / f"{slug}{e}" for e in (".csv", ".json") if (m / "data" / f"{slug}{e}").exists()), None)
        name = json.loads((m / "scenarios" / f"{slug}.create.json").read_text(encoding="utf-8"))["name"]
        items.append((name, slug, sid, src))
    items.sort(key=lambda x: x[0].lower())
    data_items = [i for i in items if i[3]]
    lines = [f"# Import test data — {title}", "",
             f"Branch `{ids['branch']['name']}` · folder scenario **{title}** · {len(data_items)} file / {len(items)} scenario (scenario không có file là scenario tĩnh, không cần import).", "",
             "Cách nhanh (Bulk Edit): mở bộ Test Data → **Bulk Edit** → dán toàn bộ nội dung file ở cột *File dán Bulk Edit* → OK (app tự lưu). Cách import file: mở scenario trong folder → tab **Test Data** → Mở bộ Test Data đã tạo sẵn (tên ở cột *Bộ Test Data*) → Import/Export → Import (đặt mã hóa **UTF-8** nếu có ô chọn) → chọn file ở cột *File* (hộp chọn file: `Cmd+Shift+G`, dán đường dẫn). Giữ nguyên tên cột (step dùng `{{tên_cột}}`).", "",
             "| # | Scenario (tên trong app) | ID | Bộ Test Data (đã tạo sẵn, rỗng) | File dán Bulk Edit | File import | Dòng |", "|---|---|---|---|---|---|---|"]
    n = 0
    for name, slug, sid, src in items:
        if not src:
            continue
        n += 1
        dst = out / f"{n:02d}-{slug}{src.suffix}"
        if src.suffix == ".csv":  # BOM UTF-8 để Apidog không đọc nhầm sang ASCII
            dst.write_text(src.read_text(encoding="utf-8-sig"), encoding="utf-8-sig", newline="")
        else:  # JSON: thoát \uXXXX, ASCII thuần nên không phụ thuộc ô chọn mã hóa
            dst.write_text(json.dumps(json.loads(src.read_text(encoding="utf-8")), ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
        bt = bulk_text(src)
        if bt is None:
            bulk_cell = "không dùng được (có ô nhiều dòng) → dùng Import"
        else:
            bdir = out / "bulk"; bdir.mkdir(exist_ok=True)
            bf = bdir / f"{n:02d}-{slug}.txt"
            bf.write_text(bt, encoding="utf-8")
            bulk_cell = f"`{bf}`"
        lines.append(f"| {n} | {name} | {sid} | `{slug}` | {bulk_cell} | `{dst}` | {rows(src)} |")
    static = [f"{name} ({sid})" for name, slug, sid, src in items if not src]
    if static:
        lines += ["", "Scenario tĩnh (không cần import): " + "; ".join(static)]
    (out / "INDEX.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    with zipfile.ZipFile(OUT / f"{slug_dir}.zip", "w", zipfile.ZIP_DEFLATED) as z:
        for f in sorted(out.rglob("*")):
            if f.is_file():
                z.write(f, f"{slug_dir}/{f.relative_to(out)}")
    summary.append((title, n, len(items), sum(rows(i[3]) for i in data_items)))
for t, n, s, r in summary:
    print(f"{t}: {n} file import, {s} scenario, {r} dòng")
