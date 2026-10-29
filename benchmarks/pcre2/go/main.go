// Command gopcre2-corpus measures the public API without cgo.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/KarpelesLab/gopcre2"
)

type testCase struct {
	ID         int          `json:"id"`
	PatternHex string       `json:"pattern_hex"`
	SubjectHex string       `json:"subject_hex"`
	Flags      gopcre2.Flag `json:"go_flags"`
	Captures   []int        `json:"captures"`
}

var sink []int

func measure(re *gopcre2.Regexp, subject string, target time.Duration) (float64, float64, float64) {
	for n := 1; ; n *= 2 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		for i := 0; i < n; i++ {
			sink = re.FindStringSubmatchIndex(subject)
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		if elapsed >= target || n >= 1<<30 {
			return float64(elapsed.Nanoseconds()) / float64(n),
				float64(after.TotalAlloc-before.TotalAlloc) / float64(n),
				float64(after.Mallocs-before.Mallocs) / float64(n)
		}
	}
}

func main() {
	corpus := flag.String("corpus", "", "exported native JSON array")
	id := flag.Int("id", 1, "upstream case number, starting at one")
	samples := flag.Int("samples", 0, "timing repetitions; zero validates only")
	duration := flag.Duration("duration", 3*time.Millisecond, "minimum duration per sample")
	profile := flag.String("cpuprofile", "", "optional CPU profile")
	flag.Parse()
	runtime.GOMAXPROCS(1)
	raw, err := os.ReadFile(*corpus)
	if err != nil {
		panic(err)
	}
	var cases []testCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		panic(err)
	}
	tc := cases[*id-1]
	pattern, err := hex.DecodeString(tc.PatternHex)
	if err != nil {
		panic(err)
	}
	subjectBytes, err := hex.DecodeString(tc.SubjectHex)
	if err != nil {
		panic(err)
	}
	subject := string(subjectBytes)
	result := map[string]any{"id": *id}
	defer func() {
		if p := recover(); p != nil {
			result["panic"] = fmt.Sprint(p)
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			panic(err)
		}
	}()
	start := time.Now()
	re, err := gopcre2.Compile(string(pattern), tc.Flags)
	result["compile_ns"] = time.Since(start).Nanoseconds()
	if err != nil {
		result["compile_error"] = err.Error()
		return
	}
	got := re.FindStringSubmatchIndex(subject)
	result["captures"] = got
	result["equal"] = reflect.DeepEqual(got, tc.Captures)
	if !reflect.DeepEqual(got, tc.Captures) || *samples == 0 {
		return
	}
	if *profile != "" {
		f, err := os.Create(*profile)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			panic(err)
		}
		defer pprof.StopCPUProfile()
	}
	// Warm up before measurement. GC remains enabled, as in real API use.
	for i := 0; i < 10; i++ {
		sink = re.FindStringSubmatchIndex(subject)
	}
	ns, bytes, allocs := []float64{}, []float64{}, []float64{}
	for i := 0; i < *samples; i++ {
		n, b, a := measure(re, subject, *duration)
		ns, bytes, allocs = append(ns, n), append(bytes, b), append(allocs, a)
	}
	result["ns"], result["bytes"], result["allocs"] = ns, bytes, allocs
}
