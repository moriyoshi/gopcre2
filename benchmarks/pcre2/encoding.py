#!/usr/bin/env python3
"""Regenerate binary-safe encoding fixtures with a pinned PCRE2 oracle."""
import argparse
import gzip
import json
from pathlib import Path
import subprocess

UTF, UCP, CASELESS, DOTALL = 1 << 9, 1 << 10, 1, 1 << 2


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--oracle", type=Path, required=True)
    parser.add_argument("--output", type=Path, default=Path("testdata/encoding.json.gz"))
    args = parser.parse_args()
    cases = []

    def add(pattern, subject, flags=0):
        if isinstance(pattern, str):
            pattern = pattern.encode()
        if isinstance(subject, str):
            subject = subject.encode()
        row = dict(pattern_hex=pattern.hex(), subject_hex=subject.hex(), flags=flags)
        row.update(json.loads(subprocess.check_output([
            str(args.oracle.resolve()), row["pattern_hex"], row["subject_hex"], str(flags)])))
        cases.append(row)

    # Same spelling, different code unit semantics; includes embedded NULs.
    for flags in [0, UTF]:
        for pattern, subject in [
            ("(.)", "é"), ("é+", "éé"), ("(é)+", "éé"),
            (r"(.)\1", "éé"), (r"[^a]+", "éa"),
            (r"[é-ö]+", "éö÷"), (r"[\xE9-\xF6]+", b"\xe9\xf6\xf7"),
            (r"[a-\x7f]+", "abz!"), (r"[\x61-z]+", "abz!"),
            (r"[a-b-c]+", "a-c!"), (r"[--0]+", "-./0!"),
            ("\x00+", b"a\x00\x00b"), (r"[\D]+", "é1"),
            ("(*UTF)(.)", "é"), ("(*UTF)(*UCP)\\w+", "é"),
        ]:
            add(pattern, subject, flags)
    # Every single byte, including malformed UTF, is valid in byte mode.
    for flags in [0, UCP, CASELESS, UCP | CASELESS]:
        for pattern in [r"(.)", r"\w", r"\W", r"[\w]", r"[\W]", r"\s", r"\h", r"\v",
                        r"\b.\b", r"\xC0", r"[\xC0-\xD6]", r"[^\xC0-\xD6]"]:
            for byte in range(256):
                add(pattern, bytes([byte]), flags)
    for flags in [0, UCP, CASELESS, UCP | CASELESS]:
        for subject in [b"\xc0\xe0", b"\xc0\xc0", b"Aa", b"\xdf\xdf", b"\xb5\xb5"]:
            add(r"(.)\1", subject, flags)
    for subject in [b"\xff", b"a\xff", b"\xc0\xaf", b"\xed\xa0\x80", b"\xf4\x90\x80\x80"]:
        for pattern in [r"(.)", r"a", r"\A"]:
            add(pattern, subject, UTF)
    for pattern in ["a(*UTF)", "a(*UCP)", r"\x{100}", r"\o{400}", r"[\x{100}]", r"[z-a]", r"[a-\d]",
                    r"\x{FFFFFFFFFFFFFFFFFFFFFFFF}", r"\x{D800}", r"\x{110000}", b"\xff"]:
        add(pattern, "", 0)
        add(pattern, "", UTF)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_bytes(gzip.compress((json.dumps(cases, separators=(",", ":")) + "\n").encode(), mtime=0))
    print(f"Wrote {len(cases)} PCRE2 encoding cases to {args.output}")


if __name__ == "__main__":
    main()
