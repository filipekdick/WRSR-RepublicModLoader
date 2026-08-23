#!/usr/bin/env python3
"""Set a PE checksum and optionally override the COFF timestamp."""
from __future__ import annotations
import argparse
import struct
from pathlib import Path


def pe_offsets(data: bytearray) -> tuple[int, int, int]:
    if len(data) < 0x100 or data[:2] != b"MZ":
        raise ValueError("not a PE file")
    pe = struct.unpack_from("<I", data, 0x3C)[0]
    if pe + 24 >= len(data) or data[pe:pe+4] != b"PE\0\0":
        raise ValueError("invalid PE signature")
    optional = pe + 24
    magic = struct.unpack_from("<H", data, optional)[0]
    if magic not in (0x10B, 0x20B):
        raise ValueError("unsupported optional header")
    return pe, pe + 8, optional + 64


def calculate_checksum(data: bytearray, checksum_offset: int) -> int:
    total = 0
    length = len(data)
    padded = data + (b"\0" if length & 1 else b"")
    for off in range(0, len(padded), 2):
        if checksum_offset <= off < checksum_offset + 4:
            word = 0
        else:
            word = padded[off] | (padded[off + 1] << 8)
        total += word
        total = (total & 0xFFFF) + (total >> 16)
    total = (total & 0xFFFF) + (total >> 16)
    total = (total & 0xFFFF) + (total >> 16)
    return (total + length) & 0xFFFFFFFF


def finalize(path: Path, timestamp: int | None) -> int:
    data = bytearray(path.read_bytes())
    _, timestamp_offset, checksum_offset = pe_offsets(data)
    if timestamp is not None:
        if timestamp < 0 or timestamp > 0xFFFFFFFF:
            raise ValueError("PE timestamp must fit in uint32")
        struct.pack_into("<I", data, timestamp_offset, timestamp)
    struct.pack_into("<I", data, checksum_offset, 0)
    checksum = calculate_checksum(data, checksum_offset)
    struct.pack_into("<I", data, checksum_offset, checksum)
    path.write_bytes(data)
    return checksum


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("files", nargs="+")
    parser.add_argument(
        "--timestamp",
        type=int,
        default=None,
        help="optional COFF timestamp override as an unsigned 32-bit integer",
    )
    args = parser.parse_args()
    timestamp = args.timestamp
    for value in args.files:
        path = Path(value)
        checksum = finalize(path, timestamp)
        print(f"{path}: checksum=0x{checksum:08X}")

if __name__ == "__main__":
    main()
