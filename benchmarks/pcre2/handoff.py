#!/usr/bin/env python3
"""Compare preserved and updated interpreters on the identical PCRE2 cases."""
import argparse
from collections import Counter
import gzip
import hashlib
import json
import math
import os
from pathlib import Path
from statistics import mean, median
import subprocess


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--before", type=Path, required=True)
    p.add_argument("--after", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--cpu", type=int, default=6)
    p.add_argument("--samples", type=int, default=3)
    p.add_argument("--resume", action="store_true")
    args = p.parse_args()
    if args.cpu in (18, 19):
        p.error("CPUs 18/19 are reserved for php2go")
    os.sched_setaffinity(0, {args.cpu})
    args.output.mkdir(parents=True, exist_ok=True)
    source = Path(__file__).resolve().parent / "results/2026-10-28-arm64/corpus.json.gz"
    corpus = args.output / "corpus.json"
    corpus.write_bytes(gzip.decompress(source.read_bytes()))
    cases = json.loads(corpus.read_text())
    env = dict(os.environ, GOMAXPROCS="1", GOWORK="off")

    def worker(binary, i, timed=False):
        command = [str(binary.resolve()), "-corpus", str(corpus.resolve()), "-id", str(i)]
        if timed:
            command += ["-samples", str(args.samples), "-duration", "3ms"]
        try:
            row = json.loads(subprocess.check_output(command, env=env, timeout=40 if timed else 3))
            row["status"] = "panic" if "panic" in row else "compile_error" if "compile_error" in row else "equal" if row.get("equal") else "mismatch"
        except subprocess.TimeoutExpired:
            row = dict(id=i, status="timeout")
        return row

    identity = dict(cpu=args.cpu, samples=args.samples, binary_sha256={k: hashlib.sha256(v.read_bytes()).hexdigest() for k,v in [("before",args.before),("after",args.after)]}, corpus_sha256=hashlib.sha256(corpus.read_bytes()).hexdigest())
    identity_path = args.output / "identity.json"
    if args.resume:
        if json.loads(identity_path.read_text()) != identity:
            raise RuntimeError("resume worker/corpus identity differs")
    else:
        identity_path.write_text(json.dumps(identity, indent=2)+"\n")
    previous = {r["id"]:r for r in map(json.loads,(args.output/"validation.jsonl").read_text().splitlines())} if args.resume else {}
    counts = {"before": Counter(), "after": Counter()}
    cohort, regressions = [], []
    with (args.output / "validation.jsonl").open("w") as f:
        for case in cases:
            if case.get("skip") or case.get("rc", -2) < -1 or case.get("jit_error") or case.get("rc") != case.get("jit_rc"):
                continue
            i = case["id"]
            if i in previous:
                a,b=previous[i]["before"],previous[i]["after"]
            else:
                a, b = worker(args.before, i), worker(args.after, i)
            for name, row in [("before", a), ("after", b)]:
                counts[name][row["status"]] += 1
            if a["status"] == b["status"] == "equal":
                cohort.append(i)
            elif a["status"] == "equal":
                regressions.append(i)
            f.write(json.dumps(dict(id=i, before=a, after=b)) + "\n")
    if regressions:
        raise RuntimeError(f"Previously passing cases regressed: {regressions}")
    samples = list(map(json.loads,(args.output/"comparison.jsonl").read_text().splitlines())) if args.resume else []
    measured = {r["id"] for r in samples}
    with (args.output / "comparison.jsonl").open("a" if args.resume else "w") as f:
        for i in cohort:
            if i in measured: continue
            a, b = worker(args.before, i, True), worker(args.after, i, True)
            if a["status"] != "equal" or b["status"] != "equal":
                raise RuntimeError(f"Timed execution changed result: {i}")
            row = dict(id=i, before=a, after=b)
            samples.append(row)
            f.write(json.dumps(row) + "\n")
    summary = dict(validation={k: dict(v) for k, v in counts.items()}, cohort=len(cohort), regressions=regressions,
        geometric_mean_speedup=math.exp(mean(math.log(median(r["before"]["ns"])/median(r["after"]["ns"])) for r in samples)),
        median_before_ns=median(median(r["before"]["ns"]) for r in samples),
        median_after_ns=median(median(r["after"]["ns"]) for r in samples),
        median_before_allocs=median(median(r["before"]["allocs"]) for r in samples),
        median_after_allocs=median(median(r["after"]["allocs"]) for r in samples))
    (args.output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    (args.output / "metadata.json").write_text(json.dumps(dict(cpu=args.cpu, samples=args.samples, sample_target="3ms",
        binary_sha256={k: hashlib.sha256(v.read_bytes()).hexdigest() for k,v in [("before",args.before),("after",args.after)]},
        corpus_sha256=hashlib.sha256(corpus.read_bytes()).hexdigest()), indent=2)+"\n")
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
