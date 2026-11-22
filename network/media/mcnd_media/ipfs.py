"""CIDv1 for raw media and bounded UnixFS files with raw 256 KiB leaves."""

import base64
import hashlib
import re


RAW_BLOCK_LIMIT = 2 << 20
CHUNK_SIZE = 256 << 10
MAX_FILE_SIZE = 10 << 20
CID = re.compile(r'(?:bafkrei|bafybei)[a-z2-7]{52}')


def _cid_bytes(data, codec):
    return bytes((1, codec, 0x12, 0x20)) + hashlib.sha256(data).digest()


def _base32_cid(raw):
    return 'b' + base64.b32encode(raw).decode('ascii').lower().rstrip('=')


def cid(data):
    return _base32_cid(_cid_bytes(data, 0x55))


def _varint(number):
    encoded = bytearray()
    while number >= 128:
        encoded.append((number & 0x7f) | 0x80)
        number >>= 7
    encoded.append(number)
    return bytes(encoded)


def _bytes_field(number, value):
    return _varint((number << 3) | 2) + _varint(len(value)) + value


def unixfs_root(data):
    # At most 40 leaves, so one balanced root suffices at full width.
    if not RAW_BLOCK_LIMIT < len(data) <= MAX_FILE_SIZE:
        raise ValueError('file is outside the bounded UnixFS media range')
    links = bytearray()
    unixfs = bytearray(b'\x08\x02\x18' + _varint(len(data)))  # Type File, filesize.
    for offset in range(0, len(data), CHUNK_SIZE):
        chunk = data[offset:offset + CHUNK_SIZE]
        # Hash, empty Name, Tsize. Links precede Data in canonical DAG-PB.
        link = _bytes_field(1, _cid_bytes(chunk, 0x55)) + b'\x12\x00\x18' + _varint(len(chunk))
        links.extend(_bytes_field(2, link))
        unixfs.extend(b'\x20' + _varint(len(chunk)))  # Unpacked UnixFS blocksizes.
    return bytes(links) + _bytes_field(1, bytes(unixfs))


def object_cid(data):
    if len(data) <= RAW_BLOCK_LIMIT:
        return cid(data)
    return _base32_cid(_cid_bytes(unixfs_root(data), 0x70))


def matches_cid(name, data):
    if not CID.fullmatch(name):
        return False
    if name.startswith('bafkrei'):
        return name == cid(data)  # Also verifies historical oversized raw images.
    return RAW_BLOCK_LIMIT < len(data) <= MAX_FILE_SIZE and name == object_cid(data)
