package replay

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"
)

func Main(compile CompileFunc) {
	input := flag.String("in", "", "normalized jsonl.gz corpus")
	iters := flag.Int("iters", 200, "passes per sample")
	samples := flag.Int("samples", 3, "samples")
	flag.Parse()
	calls, err := Load(*input)
	if err != nil {
		panic(err)
	}
	ps, err := Compile(calls, compile)
	if err != nil {
		panic(err)
	}
	if err := Validate(ps, calls); err != nil {
		panic(err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctr, err := openCounter()
	if err != nil {
		fmt.Fprintln(os.Stderr, "instruction counter unavailable:", err)
	}
	if ctr != nil {
		defer ctr.close()
	}
	for sample := 0; sample < *samples; sample++ {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		matches := 0
		start := time.Now()
		if ctr != nil {
			ctr.start()
		}
		for i := 0; i < *iters; i++ {
			for _, c := range calls {
				got, err := Run(ps, c)
				if err != nil {
					panic(err)
				}
				matches += len(got)
			}
		}
		var instructions uint64
		if ctr != nil {
			instructions = ctr.stop()
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		n := float64(*iters)
		json.NewEncoder(os.Stdout).Encode(map[string]any{"sample": sample, "calls": len(calls), "patterns": len(ps), "iterations": *iters, "matches_per_pass": float64(matches) / n, "instructions_per_pass": float64(instructions) / n, "ns_per_pass": float64(elapsed.Nanoseconds()) / n, "allocs_per_pass": float64(after.Mallocs-before.Mallocs) / n, "bytes_per_pass": float64(after.TotalAlloc-before.TotalAlloc) / n})
	}
}
