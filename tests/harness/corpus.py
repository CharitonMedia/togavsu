"""Generated Doc Tender corpus. Synthetic bytes only.

Two files are structural stand-ins and are not acceptance evidence:
the old .doc header, and the PDF that only looks encrypted.
The eight photo pages paint LETTER or LEASE and PAGE n OF m in the pixels.
"""

from __future__ import annotations

import hashlib
import json
import math
import struct
import zlib
from pathlib import Path

LEGACY_IDS = (
    "old_doc",
    "scanned_pdf_page_49",
    "merged_then_split",
    "password_pdf",
)

SYNTHETIC_PERSON = {
    "origin": "synthetic",
    "full_name": "Jordan Example",
    "email": "jordan@example.com",
    "phone": "+15555550100",
    "matter_number": "2026-0001",
}


def _sha256(payload: bytes) -> str:
    return hashlib.sha256(payload).hexdigest()


# 5x7 glyphs. A page is readable only when these pixels are on it.
_FONT = {
    " ": ["00000"] * 7,
    "0": ["01110", "10001", "10011", "10101", "11001", "10001", "01110"],
    "1": ["00100", "01100", "00100", "00100", "00100", "00100", "01110"],
    "2": ["01110", "10001", "00001", "00010", "00100", "01000", "11111"],
    "3": ["11111", "00010", "00100", "00010", "00001", "10001", "01110"],
    "4": ["00010", "00110", "01010", "10010", "11111", "00010", "00010"],
    "5": ["11111", "10000", "11110", "00001", "00001", "10001", "01110"],
    "6": ["00110", "01000", "10000", "11110", "10001", "10001", "01110"],
    "7": ["11111", "00001", "00010", "00100", "01000", "01000", "01000"],
    "8": ["01110", "10001", "10001", "01110", "10001", "10001", "01110"],
    "9": ["01110", "10001", "10001", "01111", "00001", "00010", "01100"],
    "A": ["01110", "10001", "10001", "11111", "10001", "10001", "10001"],
    "B": ["11110", "10001", "10001", "11110", "10001", "10001", "11110"],
    "C": ["01110", "10001", "10000", "10000", "10000", "10001", "01110"],
    "D": ["11110", "10001", "10001", "10001", "10001", "10001", "11110"],
    "E": ["11111", "10000", "10000", "11110", "10000", "10000", "11111"],
    "F": ["11111", "10000", "10000", "11110", "10000", "10000", "10000"],
    "G": ["01110", "10001", "10000", "10111", "10001", "10001", "01111"],
    "H": ["10001", "10001", "10001", "11111", "10001", "10001", "10001"],
    "I": ["01110", "00100", "00100", "00100", "00100", "00100", "01110"],
    "L": ["10000", "10000", "10000", "10000", "10000", "10000", "11111"],
    "O": ["01110", "10001", "10001", "10001", "10001", "10001", "01110"],
    "P": ["11110", "10001", "10001", "11110", "10000", "10000", "10000"],
    "R": ["11110", "10001", "10001", "11110", "10100", "10010", "10001"],
    "S": ["01111", "10000", "10000", "01110", "00001", "00001", "11110"],
    "T": ["11111", "00100", "00100", "00100", "00100", "00100", "00100"],
}


def _png_rows(width: int, height: int, rows: list[bytes]) -> bytes:
    def chunk(tag: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    raw = b"".join(b"\x00" + row for row in rows)
    ihdr = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")


def render_page_png(lines: list[str], scale: int = 3) -> bytes:
    """Paint uppercase text into a PNG. Solid color is not a page."""
    char_w = 5 * scale + scale
    char_h = 7 * scale
    line_gap = scale * 2
    margin = scale * 2
    width = margin * 2 + max(len(line) for line in lines) * char_w
    height = margin * 2 + len(lines) * char_h + (len(lines) - 1) * line_gap
    paper = bytes((245, 240, 230))
    ink = bytes((20, 20, 20))
    rows = [bytearray(paper * width) for _ in range(height)]
    for line_index, line in enumerate(lines):
        y0 = margin + line_index * (char_h + line_gap)
        for char_index, char in enumerate(line):
            x0 = margin + char_index * char_w
            for gy, bits in enumerate(_FONT.get(char, _FONT[" "])):
                for gx, bit in enumerate(bits):
                    if bit != "1":
                        continue
                    for sy in range(scale):
                        for sx in range(scale):
                            px = x0 + gx * scale + sx
                            py = y0 + gy * scale + sy
                            rows[py][px * 3:px * 3 + 3] = ink
    return _png_rows(width, height, [bytes(row) for row in rows])


def png_color_count(payload: bytes) -> int:
    pos = 8
    width = height = 0
    idat = b""
    while pos + 8 <= len(payload):
        length = struct.unpack(">I", payload[pos:pos + 4])[0]
        tag = payload[pos + 4:pos + 8]
        data = payload[pos + 8:pos + 8 + length]
        if tag == b"IHDR":
            width, height = struct.unpack(">II", data[:8])
        elif tag == b"IDAT":
            idat += data
        elif tag == b"IEND":
            break
        pos += 12 + length
    raw = zlib.decompress(idat)
    colors = set()
    stride = 1 + width * 3
    for y in range(height):
        row = raw[y * stride + 1:(y + 1) * stride]
        for x in range(0, len(row), 3):
            colors.add(row[x:x + 3])
            if len(colors) > 1:
                return len(colors)
    return len(colors)


PHOTO_PAGES = (
    {"page": 1, "group": "letter", "lines": ["LETTER", "PAGE 1 OF 3"]},
    {"page": 2, "group": "letter", "lines": ["LETTER", "PAGE 2 OF 3"]},
    {"page": 3, "group": "letter", "lines": ["LETTER", "PAGE 3 OF 3"]},
    {"page": 4, "group": "lease", "lines": ["LEASE", "PAGE 1 OF 5"]},
    {"page": 5, "group": "lease", "lines": ["LEASE", "PAGE 2 OF 5"]},
    {"page": 6, "group": "lease", "lines": ["LEASE", "PAGE 3 OF 5"]},
    {"page": 7, "group": "lease", "lines": ["LEASE", "PAGE 4 OF 5"]},
    {"page": 8, "group": "lease", "lines": ["LEASE", "PAGE 5 OF 5"]},
)


def _pdf(labels: list[str], mode: str, encrypt_standin: bool = False) -> bytes:
    if mode not in {"text", "image"}:
        raise ValueError(mode)
    n = len(labels)
    font_obj = 3
    next_id = 4
    kids = []
    page_parts = []
    for index, label in enumerate(labels, start=1):
        page_id = next_id
        content_id = next_id + 1
        next_id += 2
        kids.append(page_id)
        marker = f"/TogavsuSyntheticPage {index}".encode()
        if mode == "text":
            shown = f"SYNTHETIC PAGE {index} {label}".encode()
            stream = b"BT /F1 12 Tf 72 720 Td (" + shown + b") Tj ET"
            resources = f"<< /Font << /F1 {font_obj} 0 R >> >>".encode()
        else:
            stream = b"q 612 0 0 792 0 0 cm /Im0 Do Q"
            resources = f"<< /XObject << /Im0 {font_obj} 0 R >> >>".encode()
        content = b"<< /Length " + str(len(stream)).encode() + b" >>\nstream\n" + stream + b"\nendstream"
        page = (
            b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents "
            + str(content_id).encode()
            + b" 0 R /Resources "
            + resources
            + b" "
            + marker
            + b" >>"
        )
        page_parts.append(page)
        page_parts.append(content)
    kids_text = " ".join(f"{kid} 0 R" for kid in kids)
    catalog = b"<< /Type /Catalog /Pages 2 0 R >>"
    pages = f"<< /Type /Pages /Kids [{kids_text}] /Count {n} >>".encode()
    if mode == "text":
        shared = b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
    else:
        raw = bytes([80]) * 64
        shared = (
            b"<< /Type /XObject /Subtype /Image /Width 8 /Height 8 "
            b"/ColorSpace /DeviceGray /BitsPerComponent 8 /Length 64 >>\nstream\n"
            + raw
            + b"\nendstream"
        )
    ordered = [catalog, pages, shared, *page_parts]
    encrypt_id = None
    if encrypt_standin:
        ordered.append(
            b"<< /Filter /Standard /V 1 /R 2 /P -4 /O <"
            + b"00" * 32
            + b"> /U <"
            + b"11" * 32
            + b"> >>"
        )
        encrypt_id = len(ordered)
    out = bytearray(b"%PDF-1.4\n")
    offsets = [0]
    for number, body in enumerate(ordered, start=1):
        offsets.append(len(out))
        out += f"{number} 0 obj\n".encode()
        out += body
        out += b"\nendobj\n"
    xref_at = len(out)
    size = len(ordered) + 1
    out += f"xref\n0 {size}\n".encode()
    out += b"0000000000 65535 f \n"
    for offset in offsets[1:]:
        out += f"{offset:010d} 00000 n \n".encode()
    trailer = f"trailer\n<< /Size {size} /Root 1 0 R"
    if encrypt_id is not None:
        trailer += f" /Encrypt {encrypt_id} 0 R"
    trailer += " >>\n"
    out += trailer.encode()
    out += f"startxref\n{xref_at}\n%%EOF\n".encode()
    return bytes(out)


def pdf_page_numbers(payload: bytes) -> list[int]:
    import re
    return [int(item) for item in re.findall(br"/TogavsuSyntheticPage (\d+)", payload)]


def _wav() -> bytes:
    rate = 8000
    seconds = 1
    count = rate * seconds
    frames = bytearray()
    for index in range(count):
        sample = int(0.2 * 32767 * math.sin(2 * math.pi * 440 * index / rate))
        frames += struct.pack("<h", sample)
    fmt = struct.pack("<IHHIIHH", 16, 1, 1, rate, rate * 2, 2, 16)
    data = bytes(frames)
    body = b"WAVEfmt " + fmt + b"data" + struct.pack("<I", len(data)) + data
    return b"RIFF" + struct.pack("<I", len(body)) + body


def _avi_frames() -> bytes:
    width, height = 16, 16
    colors = (b"\x00\x00\xff", b"\x00\xff\x00", b"\xff\x00\x00")  # BGR red, green, blue
    frames = []
    for color in colors:
        frames.append(color * (width * height))

    def chunk(tag: bytes, payload: bytes) -> bytes:
        pad = b"\x00" if len(payload) % 2 else b""
        return tag + struct.pack("<I", len(payload)) + payload + pad

    def list_chunk(kind: bytes, payload: bytes) -> bytes:
        body = kind + payload
        return b"LIST" + struct.pack("<I", len(body)) + body

    avih = struct.pack(
        "<14I",
        1_000_000,
        width * height * 3,
        0,
        0x10,
        len(frames),
        0,
        1,
        width * height * 3,
        width,
        height,
        0,
        0,
        0,
        0,
    )
    strh = struct.pack(
        "<4s4sIHHIIIIIIIIhhhh",
        b"vids",
        b"DIB ",
        0,
        0,
        0,
        0,
        1,
        1,
        0,
        len(frames),
        width * height * 3,
        0,
        width * height * 3,
        0,
        0,
        width,
        height,
    )
    strf = struct.pack("<IiiHHIIiiII", 40, width, height, 1, 24, 0, width * height * 3, 0, 0, 0, 0)
    hdrl = list_chunk(
        b"hdrl",
        chunk(b"avih", avih) + list_chunk(b"strl", chunk(b"strh", strh) + chunk(b"strf", strf)),
    )
    movi = list_chunk(b"movi", b"".join(chunk(b"00db", frame) for frame in frames))
    body = b"AVI " + hdrl + movi
    return b"RIFF" + struct.pack("<I", len(body)) + body


def _ole_doc_standin() -> bytes:
    blob = bytearray(512)
    blob[0:8] = bytes.fromhex("D0CF11E0A1B11AE1")
    blob[0x18:0x1A] = (0x003E).to_bytes(2, "little")
    blob[0x1A:0x1C] = (0x0003).to_bytes(2, "little")
    blob[0x1C:0x1E] = (0xFFFE).to_bytes(2, "little")
    blob[0x1E:0x20] = (0x0009).to_bytes(2, "little")
    blob.extend("WordDocument".encode("utf-16le"))
    return bytes(blob)


def build_files() -> tuple[dict, dict[str, bytes]]:
    """Return the manifest and the file bytes. Nothing is read from disk."""
    files: dict[str, bytes] = {}
    entries = []

    def add(path: str, payload: bytes, **meta):
        files[path] = payload
        item = {
            "path": path,
            "sha256": _sha256(payload),
            "origin": "synthetic",
            "acceptance_evidence": meta.pop("acceptance_evidence"),
            "product_ready": meta.pop("product_ready"),
        }
        item.update(meta)
        entries.append(item)

    scanned = _pdf([f"SCAN {index}" for index in range(1, 121)], mode="image")
    add(
        "legacy/scanned-120.pdf",
        scanned,
        legacy_id="scanned_pdf_page_49",
        acceptance_evidence=True,
        product_ready=True,
        pages=120,
        inject_failure_at_page=49,
        resume_at_page=49,
        text_layer=False,
        note="120 image-only pages. A product run injects the failure at page 49 and resumes there.",
    )
    pages = []
    for spec in PHOTO_PAGES:
        path = f"legacy/merged-then-split/page-{spec['page']:02d}.png"
        pages.append({"path": path, "page": spec["page"], "group": spec["group"], "lines": list(spec["lines"])})
        add(
            path,
            render_page_png(spec["lines"]),
            legacy_id="merged_then_split",
            acceptance_evidence=True,
            product_ready=True,
            page=spec["page"],
            group=spec["group"],
            lines=list(spec["lines"]),
            page_label=" ".join(spec["lines"]),
        )
    add(
        "legacy/merged-then-split/bad-merge.pdf",
        _pdf([f"BAD MERGE {index}" for index in range(1, 9)], mode="text"),
        legacy_id="merged_then_split",
        acceptance_evidence=False,
        product_ready=True,
        role="wrong_output",
        page_groups=[8],
        note="One eight-page PDF. That is the old merged result, and it is not a pass.",
    )
    add(
        "legacy/merged-then-split/bad-split-a.pdf",
        _pdf([f"BAD SPLIT A {index}" for index in range(1, 5)], mode="text"),
        legacy_id="merged_then_split",
        acceptance_evidence=False,
        product_ready=True,
        role="wrong_output",
        pages=4,
    )
    add(
        "legacy/merged-then-split/bad-split-b.pdf",
        _pdf([f"BAD SPLIT B {index}" for index in range(1, 5)], mode="text"),
        legacy_id="merged_then_split",
        acceptance_evidence=False,
        product_ready=True,
        role="wrong_output",
        pages=4,
    )
    add(
        "legacy/old.doc",
        _ole_doc_standin(),
        legacy_id="old_doc",
        acceptance_evidence=False,
        product_ready=False,
        fidelity="structural_stand_in",
        ceiling="OLE magic, a v3 header, and the WordDocument stream name. Word and wordcraft-cli are not expected to open it.",
        upgrade="Replace these bytes with a document wordcraft-cli opens at the pinned LawCraft commit, then set product_ready true.",
        trigger="The doc-tender or editor package can shell out to wordcraft-cli and this file is rejected as an unreadable .doc rather than converted and checked.",
    )
    password = _pdf(["PASSWORD STANDIN"], mode="text", encrypt_standin=True)
    add(
        "legacy/password.pdf",
        password,
        legacy_id="password_pdf",
        acceptance_evidence=False,
        product_ready=False,
        fidelity="structural_stand_in",
        ceiling="The trailer points at a Standard encrypt dictionary. The streams are still plaintext, so a reader that ignores /Encrypt will open the page.",
        upgrade="Write a real Standard-security encrypted PDF once the product PDF library can report encryption, then set product_ready true.",
        trigger="The doc-tender package opens this file as an ordinary one-page PDF instead of an encrypted file.",
    )
    healthy = _pdf(["HEALTHY"], mode="text")
    damaged = healthy[:-40]
    add(
        "exceptions/damaged.pdf",
        damaged,
        acceptance_evidence=True,
        product_ready=True,
        kind="damaged",
        note="Truncated before the trailer. It must stay listed and go to the review queue.",
    )
    add(
        "exceptions/low-confidence.json",
        json.dumps({
            "origin": "synthetic",
            "score_thousandths": 890,
            "threshold_thousandths": 900,
            "field": "what-it-is",
        }).encode(),
        acceptance_evidence=True,
        product_ready=True,
        kind="low_confidence",
    )
    add(
        "audio/tone.wav",
        _wav(),
        acceptance_evidence=True,
        product_ready=True,
        duration_ms=1000,
        note="One second of a 440 Hz tone. There is no speech in the file.",
    )
    add(
        "video/frames.avi",
        _avi_frames(),
        acceptance_evidence=True,
        product_ready=True,
        frames=3,
        on_screen=["solid red frame", "solid green frame", "solid blue frame"],
    )
    letter = (
        "Synthetic staff upload.\n"
        "From Jordan Example <jordan@example.com>, phone +15555550100.\n"
        "Matter 2026-0001. Document date 2026-10-15.\n"
        "This is a motion to withdraw for the harness, not a client file.\n"
    )
    add(
        "sources/staff-upload.txt",
        letter.encode(),
        acceptance_evidence=True,
        product_ready=True,
        source="staff_upload",
        expected_name="example-2026-0001-motion-to-withdraw-2026-10-15.txt",
        expected_markdown="example-2026-0001-motion-to-withdraw-2026-10-15.md",
    )
    add(
        "sources/client-upload.txt",
        b"Synthetic client upload for matter 2026-0001. Uploader role client. jordan@example.com\n",
        acceptance_evidence=True,
        product_ready=True,
        source="client_upload",
    )
    add(
        "sources/drop/2026-0001/dropped.txt",
        b"Synthetic drop-folder file for matter 2026-0001.\n",
        acceptance_evidence=True,
        product_ready=True,
        source="drop_folder",
    )
    add(
        "sources/module/matter-clock.txt",
        b"Synthetic file written by the sample module for matter 2026-0001.\n",
        acceptance_evidence=True,
        product_ready=True,
        source="sample_module",
    )
    manifest = {
        "origin": "synthetic",
        "acceptance_evidence": False,
        "person": SYNTHETIC_PERSON,
        "legacy_ids": list(LEGACY_IDS),
        "expected_groups": [
            {"id": "letter", "pages": [1, 2, 3]},
            {"id": "lease", "pages": [4, 5, 6, 7, 8]},
        ],
        "wrong_split_pages": [4, 4],
        "photos": pages,
        "files": entries,
    }
    return manifest, files


def standin_hashes(manifest: dict | None = None, files: dict[str, bytes] | None = None) -> frozenset[str]:
    if manifest is None or files is None:
        manifest, files = build_files()
    hashes = []
    for item in manifest["files"]:
        if not item["product_ready"]:
            hashes.append(item["sha256"])
    return frozenset(hashes)


def write_corpus(dest: Path) -> dict:
    manifest, files = build_files()
    dest.mkdir(parents=True, exist_ok=True)
    for path, payload in files.items():
        target = dest / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(payload)
    (dest / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest
