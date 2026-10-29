#!/usr/bin/env python3
"""Export and benchmark the unmodified PCRE2 JIT regression table.

Native PCRE2 must first be built with PCRE2_SUPPORT_JIT=ON. All subprocesses
inherit CPU affinity. No corpus literals are copied into this repository.
"""
import argparse
import collections
import hashlib
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import time

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent


def run(args, **kwargs):
    return subprocess.check_output([str(a) for a in args], text=True, **kwargs)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--source", type=Path, required=True)
    p.add_argument("--build", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--samples", type=int, default=7)
    p.add_argument("--duration-ms", type=int, default=3)
    p.add_argument("--timeout", type=float, default=3)
    p.add_argument("--cpu", type=int, default=min(os.sched_getaffinity(0)))
    args = p.parse_args()
    os.sched_setaffinity(0, {args.cpu})
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=True)
    source, build = args.source.resolve(), args.build.resolve()
    upstream = source / "src/pcre2_jit_test.c"
    text = upstream.read_text()
    table = text[text.index("#define MU\t"):]
    table = table[:table.index("\n};", table.index("static struct regression_test_case regression_test_cases[]"))+3]
    # Let the C compiler handle escapes, concatenation, and #ifdefs exactly.
    (out / "corpus.h").write_text(table)
    run(["gcc", "-O3", "-DNDEBUG", "-DSUPPORT_UNICODE", "-DSUPPORT_PCRE2_8",
         "-I"+str(build / "interface"), "-I"+str(build), "-I"+str(out), HERE / "native/main.c",
         build / "libpcre2-8.a", "-o", out / "native"])
    run(["go", "build", "-o", out / "go-worker", "./benchmarks/pcre2/go"], cwd=ROOT)
    cases = [json.loads(line) for line in run([out / "native"]).splitlines()]
    corpus = out / "corpus.json"
    corpus.write_text(json.dumps(cases))
    meta = {
        "timestamp": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "platform": platform.platform(), "cpu": args.cpu,
        "go": run(["go", "version"]).strip(),
        "gcc": run(["gcc", "--version"]).splitlines()[0],
        "gopcre2_revision": run(["git", "rev-parse", "HEAD"], cwd=ROOT).strip(),
        "pcre2_revision": run(["git", "rev-parse", "HEAD"], cwd=source).strip(),
        "corpus_sha256": hashlib.sha256(upstream.read_bytes()).hexdigest(),
        "samples": args.samples, "minimum_sample_ms": args.duration_ms,
        "go_api": "FindStringSubmatchIndex", "gc": "enabled", "gomaxprocs": 1,
        "native_modes": ["pcre2_match + PCRE2_NO_JIT", "pcre2_jit_match", "pcre2_match + JIT"],
        "native_cflags": "-O3 -DNDEBUG; static 8-bit library; JIT and Unicode enabled",
        "cpu_model": Path(f"/sys/devices/system/cpu/cpu{args.cpu}/regs/identification/midr_el1").read_text().strip()
            if Path(f"/sys/devices/system/cpu/cpu{args.cpu}/regs/identification/midr_el1").exists() else None,
    }
    (out / "metadata.json").write_text(json.dumps(meta, indent=2)+"\n")
    counts = collections.Counter()
    results = []
    with (out / "results.jsonl").open("w") as log:
        for case in cases:
            row = {"id": case["id"]}
            status = case.get("skip")
            if not status and (case["rc"] < -1 or case.get("jit_error") or
                               case.get("jit_rc") != case["rc"] or
                               case.get("jit_captures") != case["captures"]):
                status = "native_error_or_disagreement"
            if not status:
                command = [out / "go-worker", "-corpus", corpus, "-id", case["id"]]
                phase = "go_validation"
                try:
                    go = json.loads(run(command, timeout=args.timeout, stderr=subprocess.PIPE))
                    row["go"] = go
                    if "panic" in go: status = "go_panic"
                    elif "compile_error" in go: status = "go_compile_error"
                    elif not go["equal"]: status = "capture_mismatch"
                    else:
                        phase = "go_benchmark"
                        row["go"] = json.loads(run(command + ["-samples", args.samples,
                            "-duration", str(args.duration_ms)+"ms"], timeout=max(10, args.timeout), stderr=subprocess.PIPE))
                        if not row["go"].get("equal") or len(row["go"].get("ns", [])) != args.samples:
                            raise RuntimeError(f"Go benchmark no longer agrees: {row['go']}")
                        phase = "native_benchmark"
                        row["native"] = json.loads(run([out / "native", case["id"],
                            args.samples, args.duration_ms*1000000], timeout=max(10, args.timeout)))
                        status = "measured"
                except subprocess.TimeoutExpired:
                    status = "go_or_native_timeout"
                    row["timeout_phase"] = phase
                except subprocess.CalledProcessError as e:
                    status = "worker_failure"
                    row["error"] = str(e.stderr)[-2000:]
            row["status"] = status
            counts[status] += 1
            results.append(row)
            log.write(json.dumps(row)+"\n")
            log.flush()
            if case["id"] % 50 == 0:
                print(f'{case["id"]}/{len(cases)}: {dict(counts)}', flush=True)
    measured = [r for r in results if r["status"] == "measured"]
    def median(r, engine):
        return statistics.median(r["go"]["ns"] if engine == "go" else r["native"][engine+"_ns"])
    ratios = sorted(median(r, "go")/median(r, "jit") for r in measured)
    summary = {"total": len(cases), "counts": dict(counts)}
    if measured:
        import math
        summary.update({
            "median_go_over_jit": statistics.median(ratios),
            "geomean_go_over_jit": math.exp(statistics.mean(math.log(x) for x in ratios)),
            "p90_go_over_jit": ratios[math.ceil(.9*len(ratios))-1],
            "sum_medians_go_over_jit": sum(median(r,"go") for r in measured)/sum(median(r,"jit") for r in measured),
            "median_go_over_interpreter": statistics.median(median(r,"go")/median(r,"interpreter") for r in measured),
            "median_go_ns": statistics.median(median(r,"go") for r in measured),
            "median_jit_ns": statistics.median(median(r,"jit") for r in measured),
            "median_bytes": statistics.median(statistics.median(r["go"]["bytes"]) for r in measured),
            "median_allocs": statistics.median(statistics.median(r["go"]["allocs"]) for r in measured),
        })
    (out / "summary.json").write_text(json.dumps(summary, indent=2)+"\n")
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
