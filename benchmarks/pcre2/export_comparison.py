#!/usr/bin/env python3
"""Save reviewable tables and compact raw evidence from compare.py's output."""
import argparse
import csv
import gzip
import hashlib
import json
from pathlib import Path
import shutil
from statistics import median
import tarfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    src, dst = args.input, args.output
    dst.mkdir(parents=True, exist_ok=True)
    cases = {c["id"]: c for c in json.loads((src / "corpus.json").read_text())}
    rows = [json.loads(line) for line in (src / "comparison.jsonl").read_text().splitlines()]
    fields = ["id", "pattern", "original_go_ns", "updated_go_ns", "jit_reuse_ns", "jit_api_ns",
              "call_control_ns", "pcre2_jit_ns", "pcre2_jit_api_ns", "reuse_allocs", "api_allocs"]
    with (dst / "cases.csv").open("w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for row in rows:
            item = dict(id=row["id"], pattern=bytes.fromhex(cases[row["id"]]["pattern_hex"]).decode("utf8", "backslashreplace"))
            for key, field in [("original_go", "original_go_ns"), ("updated_go", "updated_go_ns")]:
                if key in row:
                    item[field] = median(row[key]["ns"])
            for mode, field in [("reuse", "jit_reuse_ns"), ("api", "jit_api_ns"), ("call", "call_control_ns")]:
                item[field] = median(s["ns"] for s in row["jit"][mode])
            item.update(pcre2_jit_ns=median(row["native"]["jit_ns"]),
                        pcre2_jit_api_ns=median(row["native"]["jit_api_ns"]),
                        reuse_allocs=median(s["allocs"] for s in row["jit"]["reuse"]),
                        api_allocs=median(s["allocs"] for s in row["jit"]["api"]))
            writer.writerow(item)
    for name in ["summary.json", "metadata.json", "jit-validation.txt"]:
        shutil.copyfile(src / name, dst / name)
    for name in ["comparison.jsonl", "core-validation.jsonl", "jit-bench.txt"]:
        with gzip.GzipFile(str(dst / (name + ".gz")), "wb", mtime=0) as f:
            f.write((src / name).read_bytes())
    # Retain the actual implementation, not just an ephemeral /tmp path. The
    # committed nyanjit tree is identified by revision; retain its additions.
    metadata = json.loads((src / "metadata.json").read_text())
    snapshot = Path(metadata["source_snapshot"])
    root = snapshot / "gopcre2"
    sources = list(root.glob("*.go")) + list(root.glob("go.*"))
    sources += list((root / "jit").glob("*.go")) + list((root / "jit").glob("go.*"))
    sources += list((root / "benchmarks/pcre2/go").glob("*.go"))
    sources += list((root / "internal").rglob("*.go"))
    sources += list((root / "benchmarks/laravel/replay").glob("*.go"))
    sources += list((root / "benchmarks/laravel/testdata").glob("*.gz"))
    sources += list((root / "jit/cmd").rglob("*.go"))
    sources += [root / "benchmarks/pcre2/results/2026-10-28-arm64/corpus.json.gz"]
    if (root / "testdata/encoding.json.gz").exists():
        sources.append(root / "testdata/encoding.json.gz")
    sources += list((snapshot / "nyanjit/backend/execution").glob("leaf_func*.go"))
    repository = Path(__file__).resolve().parents[2]
    entries = {str(path.relative_to(snapshot)): path for path in sources}
    entries["gopcre2/LICENSE"] = repository / "LICENSE"
    entries["gopcre2/benchmarks/pcre2/PCRE2-CORPUS-LICENSE"] = repository / "benchmarks/pcre2/PCRE2-CORPUS-LICENSE"
    entries["nyanjit/LICENSE"] = snapshot / "nyanjit/LICENSE"
    archive = dst / "implementation.tar.gz"
    with archive.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w") as tree:
            for name, path in sorted(entries.items()):
                info = tree.gettarinfo(str(path), arcname=name)
                info.uid = info.gid = info.mtime = 0
                info.uname = info.gname = ""
                with path.open("rb") as f:
                    tree.addfile(info, f)
    metadata["implementation_archive_sha256"] = hashlib.sha256(archive.read_bytes()).hexdigest()
    (dst / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")


if __name__ == "__main__":
    main()
