"""Sinh tệp DICOM mẫu bằng tay (không phụ thuộc thư viện DICOM) cho spike D0. Dữ liệu hoàn toàn tổng hợp."""
import io, os, struct, sys
from PIL import Image

OUT = sys.argv[1]
LONG_VR = {"OB", "OD", "OF", "OL", "OV", "OW", "SQ", "UC", "UR", "UT", "UN"}

def uid_pad(s):
    b = s.encode()
    return b + (b"\0" if len(b) % 2 else b"")

def pad(b, vr):
    if len(b) % 2:
        b += b"\0" if vr in ("UI", "OB", "UN") else b" "
    return b

def el(tag, vr, value=b"", explicit=True, undefined=False):
    g, e = tag
    if isinstance(value, str):
        value = value.encode()
    if vr != "SQ":
        value = pad(value, vr)
    if not explicit:
        return struct.pack("<HHI", g, e, len(value)) + value
    if vr in LONG_VR:
        ln = 0xFFFFFFFF if undefined else len(value)
        return struct.pack("<HH", g, e) + vr.encode() + b"\0\0" + struct.pack("<I", ln) + value
    return struct.pack("<HH", g, e) + vr.encode() + struct.pack("<H", len(value)) + value

def item(content, undefined=False):
    if undefined:
        return struct.pack("<HHI", 0xFFFE, 0xE000, 0xFFFFFFFF) + content + struct.pack("<HHI", 0xFFFE, 0xE00D, 0)
    return struct.pack("<HHI", 0xFFFE, 0xE000, len(content)) + content

def seq_end():
    return struct.pack("<HHI", 0xFFFE, 0xE0DD, 0)

def us(v): return struct.pack("<H", v)

def meta(ts, sop_class, sop_uid):
    body = (el((2, 1), "OB", b"\0\1") + el((2, 2), "UI", sop_class) + el((2, 3), "UI", sop_uid) +
            el((2, 0x10), "UI", ts) + el((2, 0x12), "UI", "1.2.826.0.1.3680043.8.498.1") + el((2, 0x13), "SH", "SPIKE"))
    return b"\0" * 128 + b"DICM" + el((2, 0), "UL", struct.pack("<I", len(body))) + body

def dataset(explicit, study, series, sop, nframes, rows, cols, bits, pixel, undefined_sq=False, modality="CT", big_ob=0, overlay=False):
    e = explicit
    d = b""
    d += el((8, 0x16), "UI", "1.2.840.10008.5.1.4.1.1.2", e)
    d += el((8, 0x18), "UI", sop, e)
    d += el((8, 0x20), "DA", "20260101", e)
    d += el((8, 0x30), "TM", "101500", e)
    d += el((8, 0x50), "SH", "ACC123", e)
    d += el((8, 0x60), "CS", modality, e)
    d += el((8, 0x90), "PN", "Nguyen^Bac Si^^Dr", e)
    ref = item(el((8, 0x1150), "UI", "1.2.840.10008.3.1.2.3.1", e) + el((8, 0x1155), "UI", "1.2.3.4.5", e), undefined_sq)
    d += el((8, 0x1110), "SQ", ref + (seq_end() if undefined_sq else b""), e, undefined=undefined_sq) if e else \
         el((8, 0x1110), "SQ", ref + (seq_end() if undefined_sq else b""), e)
    d += el((0x10, 0x10), "PN", "Tran^Van A", e)
    d += el((0x10, 0x20), "LO", "PID-0001", e)
    d += el((0x10, 0x30), "DA", "19800101", e)
    d += el((0x10, 0x40), "CS", "M", e)
    d += el((0x20, 0xD), "UI", study, e)
    d += el((0x20, 0xE), "UI", series, e)
    d += el((0x20, 0x10), "SH", "STUDY1", e)
    d += el((0x20, 0x11), "IS", "1", e)
    d += el((0x20, 0x13), "IS", "1", e)
    if big_ob:
        d += el((0x0009, 0x1001), "OB", os.urandom(big_ob), e) if e else el((0x0009, 0x1001), "OB", os.urandom(big_ob), e)
    d += el((0x28, 2), "US", us(1), e)
    d += el((0x28, 4), "CS", "MONOCHROME2" if bits != 24 else "RGB", e)
    if nframes > 1:
        d += el((0x28, 8), "IS", str(nframes), e)
    d += el((0x28, 0x10), "US", us(rows), e)
    d += el((0x28, 0x11), "US", us(cols), e)
    d += el((0x28, 0x100), "US", us(bits if bits != 24 else 8), e)
    d += el((0x28, 0x101), "US", us(bits if bits != 24 else 8), e)
    d += el((0x28, 0x102), "US", us((bits if bits != 24 else 8) - 1), e)
    d += el((0x28, 0x103), "US", us(0), e)
    if overlay:
        d += el((0x6000, 0x3000), "OW", b"\xff" * 64, e)
    return d

def write(name, explicit, ts, nframes=1, rows=32, cols=32, bits=16, encap=None, **kw):
    study, series = "1.2.826.0.1.3680043.8.498.100", "1.2.826.0.1.3680043.8.498.100.1"
    sop = "1.2.826.0.1.3680043.8.498.100.1." + name.split(".")[0].replace("_", "")[:0] + str(abs(hash(name)) % 100000)
    sop = "1.2.826.0.1.3680043.8.498.%d" % (abs(hash(name)) % 10**9)
    ds = dataset(explicit, study, series, sop, nframes, rows, cols, bits, None, **kw)
    if encap is not None:
        # PixelData đóng gói: Basic Offset Table rỗng + mỗi frame một fragment
        frags = b"".join(struct.pack("<HHI", 0xFFFE, 0xE000, len(f) + len(f) % 2) + f + (b"\0" if len(f) % 2 else b"") for f in encap)
        bot = struct.pack("<HHI", 0xFFFE, 0xE000, 0)
        ds += struct.pack("<HH", 0x7FE0, 0x10) + b"OB\0\0" + struct.pack("<I", 0xFFFFFFFF) + bot + frags + seq_end()
    else:
        nbytes = rows * cols * nframes * (bits // 8 if bits != 24 else 3)
        px = bytes((i * 7 + (i >> 8)) & 0xFF for i in range(min(nbytes, 1 << 16)))
        px = (px * (nbytes // len(px) + 1))[:nbytes]
        if kw.get("pixel_size"):
            pass
        ds += el((0x7FE0, 0x10), "OW" if bits > 8 else "OB", px, explicit) if explicit else el((0x7FE0, 0x10), "OW", px, False)
    with open(os.path.join(OUT, name), "wb") as f:
        f.write(meta(ts, "1.2.840.10008.5.1.4.1.1.2", sop) + ds)
    return sop

def jpeg(frame_i, size=(32, 32), fmt="JPEG"):
    im = Image.new("L", size)
    im.putdata([(x * 5 + frame_i * 40) & 0xFF for x in range(size[0] * size[1])])
    b = io.BytesIO(); im.save(b, fmt); return b.getvalue()

write("unc_1frame.dcm", True, "1.2.840.10008.1.2.1", big_ob=20000, overlay=True)
write("unc_multiframe.dcm", True, "1.2.840.10008.1.2.1", nframes=8, bits=8)
write("implicit_le.dcm", False, "1.2.840.10008.1.2")
write("undefined_sq.dcm", True, "1.2.840.10008.1.2.1", undefined_sq=True)
write("jpeg_baseline.dcm", True, "1.2.840.10008.1.2.4.50", nframes=3, bits=8, encap=[jpeg(i) for i in range(3)])
try:
    j2k = []
    for i in range(2):
        im = Image.new("L", (32, 32)); im.putdata([(x * 3 + i * 50) & 0xFF for x in range(1024)])
        b = io.BytesIO(); im.save(b, "JPEG2000"); j2k.append(b.getvalue())
    write("jpeg2000.dcm", True, "1.2.840.10008.1.2.4.90", nframes=2, bits=8, encap=j2k)
except Exception as ex:
    print("JPEG2000 không tạo được:", ex)
# tệp lớn: ~120 MB PixelData (2048x2048x16 bit x 15 frame), ghi luồng để không tốn RAM
rows = cols = 2048; nfr = 15
study, series = "1.2.826.0.1.3680043.8.498.100", "1.2.826.0.1.3680043.8.498.100.9"
sop = "1.2.826.0.1.3680043.8.498.999"
ds = dataset(True, study, series, sop, nfr, rows, cols, 16, None)
n = rows * cols * nfr * 2
with open(os.path.join(OUT, "big_120mb.dcm"), "wb") as f:
    f.write(meta("1.2.840.10008.1.2.1", "1.2.840.10008.5.1.4.1.1.2", sop) + ds)
    f.write(struct.pack("<HH", 0x7FE0, 0x10) + b"OW\0\0" + struct.pack("<I", n))
    chunk = bytes(range(256)) * 4096
    left = n
    while left:
        w = min(left, len(chunk)); f.write(chunk[:w]); left -= w
# hỏng
good = open(os.path.join(OUT, "unc_1frame.dcm"), "rb").read()
open(os.path.join(OUT, "corrupt_trunc_header.dcm"), "wb").write(good[:300])
open(os.path.join(OUT, "corrupt_trunc_pixel.dcm"), "wb").write(good[:len(good) - 500])
open(os.path.join(OUT, "corrupt_garbage.dcm"), "wb").write(os.urandom(5000))
open(os.path.join(OUT, "not_dicom.txt"), "wb").write(b"hello world" * 100)
