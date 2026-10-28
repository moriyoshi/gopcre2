#!/usr/bin/env python3
"""Save replay measurements, medians, worker identities and source checkpoints."""
import argparse
import csv
import gzip
import hashlib
import json
from pathlib import Path
import shutil
from statistics import median
import tarfile


def archive_source(root, target):
    paths = list(root.glob("*.go")) + list(root.glob("go.*")) + [root / "LICENSE"]
    for folder in ["internal", "benchmarks/laravel/replay", "benchmarks/pcre2/go", "jit"]:
        paths += list((root / folder).rglob("*.go"))
    paths += list((root / "jit").glob("go.*"))
    paths += list((root / "testdata").glob("*.gz"))
    paths += list((root / "benchmarks/laravel/testdata").glob("*.gz"))
    paths += [root / "benchmarks/pcre2/results/2026-10-28-arm64/corpus.json.gz"]
    entries = {str(path.relative_to(root)): path for path in paths if path.is_file()}
    repository = Path(__file__).resolve().parents[2]
    entries["LICENSE"] = repository / "LICENSE"
    entries["benchmarks/pcre2/PCRE2-CORPUS-LICENSE"] = repository / "benchmarks/pcre2/PCRE2-CORPUS-LICENSE"
    with target.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w") as tree:
            for name, path in sorted(entries.items()):
                info = tree.gettarinfo(str(path), arcname=name)
                info.uid = info.gid = info.mtime = 0
                info.uname = info.gname = ""
                with path.open("rb") as f:
                    tree.addfile(info, f)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("input", type=Path)
    p.add_argument("output", type=Path)
    p.add_argument("--before-source", type=Path, required=True)
    p.add_argument("--after-source", type=Path, required=True)
    args = p.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    summary = {}
    for path in sorted(args.input.glob("*.json*")):
        shutil.copyfile(path, args.output / path.name)
    for route in ["eloquent", "json"]:
        summary[route] = {}
        for stage in ["before", "pooled", "filtered", "jit"]:
            rows = [json.loads(line) for line in (args.input / (route + "-" + stage + ".jsonl")).read_text().splitlines()]
            summary[route][stage] = {key: median(r[key] for r in rows) for key in ["instructions_per_pass", "ns_per_pass", "allocs_per_pass", "bytes_per_pass", "matches_per_pass"]}
    (args.output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    with (args.output / "stages.csv").open("w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["route", "stage", *next(iter(summary["eloquent"].values())).keys()])
        for route, stages in summary.items():
            for stage, row in stages.items():
                writer.writerow([route, stage, *row.values()])
    sources = {}
    for stage, root in [("before", args.before_source), ("after", args.after_source)]:
        path = args.output / (stage + "-source.tar.gz")
        archive_source(root, path)
        sources[stage] = hashlib.sha256(path.read_bytes()).hexdigest()
    (args.output / "source-sha256.json").write_text(json.dumps(sources, indent=2) + "\n")


if __name__ == "__main__":
    main()
