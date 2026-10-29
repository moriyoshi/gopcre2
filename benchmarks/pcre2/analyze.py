#!/usr/bin/env python3
"""Make reviewable tables and compact raw evidence from run.py's output."""
import argparse
import collections
import csv
import gzip
import json
from pathlib import Path
import re
import shutil
import statistics as stats


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("input", type=Path)
    p.add_argument("output", type=Path)
    args = p.parse_args()
    src, dst = args.input, args.output
    dst.mkdir(parents=True, exist_ok=True)
    cases = json.loads((src/"corpus.json").read_text())
    rows = [json.loads(line) for line in (src/"results.jsonl").read_text().splitlines()]
    groups = []
    group = ""
    for line in (src/"corpus.h").read_text().splitlines():
        comment = re.fullmatch(r"\s*/\* (.*?) \*/", line)
        if comment:
            group = comment[1].rstrip(".")
        if re.match(r"\s*\{ ", line) and "NULL, NULL" not in line:
            groups.append(group)
    assert len(groups) == len(cases) == len(rows)
    med = stats.median
    by_group = collections.defaultdict(list)
    fields = ["id", "group", "status", "pattern", "subject_bytes", "go_ns", "native_interpreter_ns",
              "native_jit_ns", "native_jit_api_ns", "go_over_jit", "bytes", "allocs",
              "expected_captures", "go_captures", "go_compile_error"]
    with (dst/"cases.csv").open("w") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for case, row, group in zip(cases, rows, groups):
            assert case["id"] == row["id"]
            item = {
                "id": case["id"], "group": group, "status": row["status"],
                "pattern": bytes.fromhex(case["pattern_hex"]).decode("utf8", "backslashreplace"),
                "subject_bytes": len(bytes.fromhex(case["subject_hex"])),
                "expected_captures": json.dumps(case.get("captures")),
                "go_captures": json.dumps(row.get("go", {}).get("captures")),
                "go_compile_error": row.get("go", {}).get("compile_error", ""),
            }
            if row["status"] == "measured":
                item.update({"go_ns": med(row["go"]["ns"]),
                    "native_interpreter_ns": med(row["native"]["interpreter_ns"]),
                    "native_jit_ns": med(row["native"]["jit_ns"]),
                    "native_jit_api_ns": med(row["native"]["jit_api_ns"]),
                    "go_over_jit": med(row["go"]["ns"])/med(row["native"]["jit_ns"]),
                    "bytes": med(row["go"]["bytes"]), "allocs": med(row["go"]["allocs"])})
            writer.writerow(item)
            by_group[group].append(item)
    with (dst/"groups.csv").open("w") as f:
        writer = csv.writer(f)
        writer.writerow(["group", "total", "measured", "median_go_over_jit", "median_go_ns", "median_jit_ns"])
        for group, items in by_group.items():
            measured = [r for r in items if r["status"] == "measured"]
            writer.writerow([group, len(items), len(measured)] +
                ([med(r[k] for r in measured) for k in ["go_over_jit", "go_ns", "native_jit_ns"]] if measured else ["", "", ""]))
    for name in ["metadata.json", "summary.json"]:
        shutil.copyfile(src/name, dst/name)
    for name in ["corpus.json", "results.jsonl"]:
        with gzip.GzipFile(str(dst/(name+".gz")), "wb", mtime=0) as f:
            f.write((src/name).read_bytes())


if __name__ == "__main__":
    main()
