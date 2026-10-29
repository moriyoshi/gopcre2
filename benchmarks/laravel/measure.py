#!/usr/bin/env python3
"""Measure a built replay worker on the same corpus, CPU and native oracle."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--binary", type=Path, required=True)
    p.add_argument("--stage", required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--cpu", type=int, default=6)
    p.add_argument("--iters", type=int, default=200)
    p.add_argument("--samples", type=int, default=3)
    p.add_argument("--image", default="php2go-native-sym:8.5.11")
    args = p.parse_args()
    if args.cpu in (18, 19):
        p.error("CPUs 18 and 19 are reserved for php2go")
    available = int(next(line.split()[1] for line in Path("/proc/meminfo").read_text().splitlines() if line.startswith("MemAvailable:")))
    if available < 3 * 1024 * 1024:
        p.error("less than 3 GiB available for the bounded replay container")
    args.output.mkdir(parents=True, exist_ok=True)
    data = Path(__file__).resolve().parent / "testdata"
    metadata = dict(cpu=args.cpu, iterations=args.iters, samples=args.samples,
                    binary_sha256=hashlib.sha256(args.binary.read_bytes()).hexdigest(),
                    available_memory_kib=available, commands={}, corpus_sha256={})
    metadata["image"] = subprocess.check_output(["docker", "image", "inspect", args.image, "--format", "{{.Id}}"], text=True).strip()
    for route in ["eloquent", "json"]:
        metadata["corpus_sha256"][route] = hashlib.sha256((data / (route + ".jsonl.gz")).read_bytes()).hexdigest()
        cmd = ["docker", "run", "--rm", "--network", "none", "--memory", "2g", "--cap-add", "PERFMON", "--cpuset-cpus", str(args.cpu),
               "-e", "GOMAXPROCS=1", "-v", str(args.binary.resolve()) + ":/replay:ro", "-v", str(data) + ":/data:ro",
               "--entrypoint", "/replay", args.image, "-in", "/data/" + route + ".jsonl.gz", "-iters", str(args.iters), "-samples", str(args.samples)]
        metadata["commands"][route] = cmd
        with (args.output / (route + "-" + args.stage + ".jsonl")).open("w") as f:
            subprocess.run(cmd, stdout=f, check=True)
        rows = [json.loads(line) for line in (args.output / (route + "-" + args.stage + ".jsonl")).read_text().splitlines()]
        if len(rows) != args.samples or any(r["instructions_per_pass"] <= 0 for r in rows):
            raise RuntimeError("instruction counter did not produce valid samples on the selected CPU")
        print(route, args.stage, "complete", flush=True)
    (args.output / (args.stage + "-metadata.json")).write_text(json.dumps(metadata, indent=2) + "\n")


if __name__ == "__main__":
    main()
