#!/usr/bin/env python3
"""Embed Windows icon and VERSIONINFO resources in a PE32+ image.

RML uses this after linking so the Go launcher and native runtime share the
same release metadata without checked-in generated resource objects.
"""
from __future__ import annotations

import argparse
import struct
from pathlib import Path

RT_ICON = 3
RT_GROUP_ICON = 14
RT_VERSION = 16
LANG_EN_US = 0x0409
CODEPAGE_UNICODE = 1200
SCN_CNT_INITIALIZED_DATA = 0x00000040
SCN_MEM_READ = 0x40000000


def align(value: int, boundary: int) -> int:
    return (value + boundary - 1) & ~(boundary - 1)


def utf16z(text: str) -> bytes:
    return text.encode("utf-16le") + b"\0\0"


def version_tuple(text: str) -> tuple[int, int, int, int]:
    parts = text.strip().split(".")
    if not 1 <= len(parts) <= 4:
        raise ValueError("version must contain one to four numeric components")
    nums = [int(p) for p in parts]
    if any(n < 0 or n > 0xFFFF for n in nums):
        raise ValueError("version components must fit in uint16")
    nums += [0] * (4 - len(nums))
    return tuple(nums)  # type: ignore[return-value]


def make_version_block(key: str, value: bytes, value_len: int, value_type: int, children: list[bytes] | None = None) -> bytes:
    children = children or []
    buf = bytearray(b"\0" * 6)
    buf += utf16z(key)
    while len(buf) % 4:
        buf.append(0)
    buf += value
    while len(buf) % 4:
        buf.append(0)
    for child in children:
        buf += child
    struct.pack_into("<HHH", buf, 0, len(buf), value_len, value_type)
    return bytes(buf)


def make_string(name: str, value: str) -> bytes:
    encoded = utf16z(value)
    return make_version_block(name, encoded, len(encoded) // 2, 1)


def make_version_info(version: str, file_description: str, original_filename: str,
                      product_name: str, company_name: str, copyright_text: str,
                      comments: str, is_dll: bool) -> bytes:
    major, minor, patch, build = version_tuple(version)
    ms = (major << 16) | minor
    ls = (patch << 16) | build
    fixed = struct.pack(
        "<13I",
        0xFEEF04BD, 0x00010000,
        ms, ls, ms, ls,
        0x0000003F, 0,
        0x00040004,
        0x00000002 if is_dll else 0x00000001,
        0, 0, 0,
    )

    string_values = [
        ("CompanyName", company_name),
        ("FileDescription", file_description),
        ("FileVersion", version),
        ("InternalName", Path(original_filename).stem),
        ("LegalCopyright", copyright_text),
        ("OriginalFilename", original_filename),
        ("ProductName", product_name),
        ("ProductVersion", version),
    ]
    if comments:
        string_values.append(("Comments", comments))

    table = make_version_block(
        "040904B0", b"", 0, 1,
        [make_string(k, v) for k, v in string_values],
    )
    string_file_info = make_version_block("StringFileInfo", b"", 0, 1, [table])
    translation = make_version_block("Translation", struct.pack("<HH", LANG_EN_US, CODEPAGE_UNICODE), 4, 0)
    var_file_info = make_version_block("VarFileInfo", b"", 0, 1, [translation])
    return make_version_block("VS_VERSION_INFO", fixed, len(fixed), 0, [string_file_info, var_file_info])


def parse_ico(path: Path):
    data = path.read_bytes()
    if len(data) < 6:
        raise ValueError("ICO is truncated")
    reserved, kind, count = struct.unpack_from("<HHH", data, 0)
    if reserved != 0 or kind != 1 or count < 1:
        raise ValueError("not a Windows icon file")
    entries = []
    pos = 6
    for i in range(count):
        if pos + 16 > len(data):
            raise ValueError("ICO directory is truncated")
        w, h, colors, reserved2, planes, bpp, size, off = struct.unpack_from("<BBBBHHII", data, pos)
        pos += 16
        if off + size > len(data):
            raise ValueError(f"ICO frame {i} is truncated")
        entries.append({
            "width": w, "height": h, "colors": colors,
            "planes": planes, "bpp": bpp, "size": size,
            "data": data[off:off + size], "id": i + 1,
        })
    return entries


class ResourceBuilder:
    def __init__(self, section_rva: int, icons, version_blob: bytes | None):
        self.section_rva = section_rva
        self.icons = icons
        self.version_blob = version_blob
        self.buf = bytearray()

    def reserve(self, size: int, alignment: int = 4) -> int:
        while len(self.buf) % alignment:
            self.buf.append(0)
        off = len(self.buf)
        self.buf.extend(b"\0" * size)
        return off

    def dir_header(self, off: int, id_entries: int) -> None:
        struct.pack_into("<IIHHHH", self.buf, off, 0, 0, 0, 0, 0, id_entries)

    def build(self) -> bytes:
        root_types = []
        if self.icons:
            root_types.extend([RT_ICON, RT_GROUP_ICON])
        if self.version_blob is not None:
            root_types.append(RT_VERSION)
        root_types.sort()
        if not root_types:
            raise ValueError("at least one resource is required")

        root = self.reserve(16 + len(root_types) * 8)
        type_dirs: dict[int, int] = {}
        lang_dirs: dict[tuple[int, int], int] = {}
        data_entries: dict[tuple[int, int], int] = {}

        for resource_type in root_types:
            count = len(self.icons) if resource_type == RT_ICON else 1
            type_dirs[resource_type] = self.reserve(16 + count * 8)
            for item_id in range(1, count + 1):
                lang_dirs[(resource_type, item_id)] = self.reserve(16 + 8)
                data_entries[(resource_type, item_id)] = self.reserve(16)

        icon_blob_offsets = []
        for frame in self.icons:
            off = self.reserve(len(frame["data"]), 4)
            self.buf[off:off + len(frame["data"])] = frame["data"]
            icon_blob_offsets.append(off)

        group_blob = b""
        group_blob_off = 0
        if self.icons:
            group = bytearray(struct.pack("<HHH", 0, 1, len(self.icons)))
            for frame in self.icons:
                group += struct.pack(
                    "<BBBBHHIH",
                    frame["width"], frame["height"], frame["colors"], 0,
                    frame["planes"], frame["bpp"], frame["size"], frame["id"],
                )
            group_blob = bytes(group)
            group_blob_off = self.reserve(len(group_blob), 4)
            self.buf[group_blob_off:group_blob_off + len(group_blob)] = group_blob

        version_blob_off = 0
        if self.version_blob is not None:
            version_blob_off = self.reserve(len(self.version_blob), 4)
            self.buf[version_blob_off:version_blob_off + len(self.version_blob)] = self.version_blob

        self.dir_header(root, len(root_types))
        for i, resource_type in enumerate(root_types):
            struct.pack_into("<II", self.buf, root + 16 + i * 8, resource_type, 0x80000000 | type_dirs[resource_type])

        for resource_type in root_types:
            count = len(self.icons) if resource_type == RT_ICON else 1
            tdir = type_dirs[resource_type]
            self.dir_header(tdir, count)
            for item_id in range(1, count + 1):
                struct.pack_into("<II", self.buf, tdir + 16 + (item_id - 1) * 8,
                                 item_id, 0x80000000 | lang_dirs[(resource_type, item_id)])
                ldir = lang_dirs[(resource_type, item_id)]
                self.dir_header(ldir, 1)
                struct.pack_into("<II", self.buf, ldir + 16, LANG_EN_US, data_entries[(resource_type, item_id)])

        if self.icons:
            for i, frame in enumerate(self.icons, start=1):
                struct.pack_into("<IIII", self.buf, data_entries[(RT_ICON, i)],
                                 self.section_rva + icon_blob_offsets[i - 1], frame["size"], 0, 0)
            struct.pack_into("<IIII", self.buf, data_entries[(RT_GROUP_ICON, 1)],
                             self.section_rva + group_blob_off, len(group_blob), 0, 0)

        if self.version_blob is not None:
            struct.pack_into("<IIII", self.buf, data_entries[(RT_VERSION, 1)],
                             self.section_rva + version_blob_off, len(self.version_blob), CODEPAGE_UNICODE, 0)

        return bytes(self.buf)


def inject_resources(pe_path: Path, icon_path: Path | None, version_blob: bytes | None) -> None:
    icons = parse_ico(icon_path) if icon_path else []
    data = bytearray(pe_path.read_bytes())
    if len(data) < 0x200 or data[:2] != b"MZ":
        raise ValueError("not a PE file")
    pe = struct.unpack_from("<I", data, 0x3C)[0]
    if data[pe:pe + 4] != b"PE\0\0":
        raise ValueError("invalid PE signature")
    nsec = struct.unpack_from("<H", data, pe + 6)[0]
    opt_size = struct.unpack_from("<H", data, pe + 20)[0]
    opt = pe + 24
    if struct.unpack_from("<H", data, opt)[0] != 0x20B:
        raise ValueError("expected PE32+ image")
    file_align = struct.unpack_from("<I", data, opt + 36)[0]
    section_align = struct.unpack_from("<I", data, opt + 32)[0]
    size_headers = struct.unpack_from("<I", data, opt + 60)[0]
    size_image = struct.unpack_from("<I", data, opt + 56)[0]
    resource_dir_off = opt + 112 + 2 * 8
    if struct.unpack_from("<II", data, resource_dir_off) != (0, 0):
        raise ValueError("PE already contains resources; refusing to overwrite them")

    section_table = opt + opt_size
    new_header = section_table + nsec * 40
    if new_header + 40 > size_headers:
        raise ValueError("no room in PE headers for another section")

    max_raw_end = 0
    for i in range(nsec):
        off = section_table + i * 40
        raw_size, raw_off = struct.unpack_from("<II", data, off + 16)
        max_raw_end = max(max_raw_end, raw_off + raw_size)
    if max_raw_end != len(data):
        raise ValueError("PE has an overlay; resource injector will not relocate it")

    section_rva = align(size_image, section_align)
    rsrc = ResourceBuilder(section_rva, icons, version_blob).build()
    raw_off = align(len(data), file_align)
    raw_size = align(len(rsrc), file_align)

    if len(data) < raw_off:
        data.extend(b"\0" * (raw_off - len(data)))
    data.extend(rsrc)
    if len(rsrc) < raw_size:
        data.extend(b"\0" * (raw_size - len(rsrc)))

    data[new_header:new_header + 8] = b".rsrc\0\0\0"
    struct.pack_into("<IIIIIIHHI", data, new_header + 8,
                     len(rsrc), section_rva, raw_size, raw_off,
                     0, 0, 0, 0, SCN_CNT_INITIALIZED_DATA | SCN_MEM_READ)
    struct.pack_into("<H", data, pe + 6, nsec + 1)
    struct.pack_into("<II", data, resource_dir_off, section_rva, len(rsrc))
    struct.pack_into("<I", data, opt + 56, align(section_rva + len(rsrc), section_align))
    old_init = struct.unpack_from("<I", data, opt + 8)[0]
    struct.pack_into("<I", data, opt + 8, old_init + raw_size)

    pe_path.write_bytes(data)
    print(f"{pe_path}: embedded resources in .rsrc at RVA 0x{section_rva:X}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("image", type=Path)
    parser.add_argument("--icon", type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--file-description", required=True)
    parser.add_argument("--original-filename", required=True)
    parser.add_argument("--product-name", default="Republic Mod Loader")
    parser.add_argument("--company-name", default="Ultimate-Universe")
    parser.add_argument("--copyright", default="Copyright (C) 2026 Ultimate-Universe")
    parser.add_argument("--comments", default="RML API 1")
    parser.add_argument("--dll", action="store_true")
    args = parser.parse_args()

    version_blob = make_version_info(
        args.version, args.file_description, args.original_filename,
        args.product_name, args.company_name, args.copyright,
        args.comments, args.dll,
    )
    inject_resources(args.image, args.icon, version_blob)


if __name__ == "__main__":
    main()
