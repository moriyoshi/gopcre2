package jit

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"testing"

	p "github.com/KarpelesLab/gopcre2"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

var corpusBenchCase = flag.Int("pcre2-bench-case", 0, "benchmark only this PCRE2 corpus case (zero selects all)")

type corpusCase struct {
	ID       int    `json:"id"`
	Pattern  string `json:"pattern_hex"`
	Subject  string `json:"subject_hex"`
	Flags    p.Flag `json:"go_flags"`
	Captures []int  `json:"captures"`
	Skip     string `json:"skip"`
	RC       int    `json:"rc"`
	JITRC    int    `json:"jit_rc"`
	JITError int    `json:"jit_error"`
}

func loadCorpus(t testing.TB) []corpusCase {
	t.Helper()
	f, err := os.Open("../benchmarks/pcre2/results/2026-10-28-arm64/corpus.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var cases []corpusCase
	if err := json.NewDecoder(z).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestPCRE2Corpus(t *testing.T) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("native corpus requires Linux amd64 or arm64")
	}
	matched, unsupported, subject, syntax := 0, 0, 0, 0
	var nativeIDs []int
	for _, tc := range loadCorpus(t) {
		if tc.Skip != "" || tc.RC < -1 || tc.JITError != 0 || tc.RC != tc.JITRC {
			continue
		}
		pattern, _ := hex.DecodeString(tc.Pattern)
		input, _ := hex.DecodeString(tc.Subject)
		r, err := Compile(string(pattern), tc.Flags)
		if err != nil {
			syntax++
			continue
		}
		if !r.Native() {
			if !errors.Is(r.NativeError(), ErrUnsupported) && !errors.Is(r.NativeError(), compile.ErrBackendLimit) {
				t.Errorf("case %d %q: native compilation failed: %v", tc.ID, pattern, r.NativeError())
			}
			unsupported++
			r.Close()
			continue
		}
		matcher := r.NewMatcher()
		got, err := matcher.Find(string(input), 0)
		if errors.Is(err, ErrSubject) {
			subject++
			r.Close()
			continue
		}
		for repeat := 0; repeat < 16; repeat++ {
			if repeat > 0 {
				got, err = matcher.Find(string(input), 0)
			}
			if err != nil || !reflect.DeepEqual(got, tc.Captures) {
				t.Errorf("case %d %q @ %q, repetition %d: got %v, %v; PCRE2 %v", tc.ID, pattern, input, repeat, got, err, tc.Captures)
				break
			}
		}
		matched++
		nativeIDs = append(nativeIDs, tc.ID)
		r.Close()
	}
	t.Logf("PCRE2 corpus: %d native comparisons, %d unsupported patterns, %d non-ASCII UTF subjects, %d parser rejections", matched, unsupported, subject, syntax)
	t.Logf("Native case IDs: %v", nativeIDs)
	if matched < 80 {
		t.Errorf("native corpus coverage fell unexpectedly: %d", matched)
	}
}

func BenchmarkPCRE2Corpus(b *testing.B) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		b.Skip("native corpus requires Linux amd64 or arm64")
	}
	for _, tc := range loadCorpus(b) {
		if *corpusBenchCase != 0 && tc.ID != *corpusBenchCase {
			continue
		}
		if tc.Skip != "" || tc.RC < -1 || tc.JITError != 0 || tc.RC != tc.JITRC {
			continue
		}
		pattern, _ := hex.DecodeString(tc.Pattern)
		input, _ := hex.DecodeString(tc.Subject)
		r, err := Compile(string(pattern), tc.Flags)
		if err != nil {
			continue
		}
		if !r.Native() {
			r.Close()
			continue
		}
		m := r.NewMatcher()
		s := string(input)
		got, err := m.Find(s, 0)
		if errors.Is(err, ErrSubject) {
			r.Close()
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tc.Captures) {
			r.Close()
			b.Fatalf("case %d differs from PCRE2", tc.ID)
		}
		b.Run(fmt.Sprintf("%03d", tc.ID), func(b *testing.B) {
			b.Run("reuse", func(b *testing.B) {
				b.ReportAllocs()
				var last []int
				for b.Loop() {
					var err error
					last, err = m.Find(s, 0)
					if err != nil {
						b.Fatal(err)
					}
				}
				if !reflect.DeepEqual(last, tc.Captures) {
					b.Fatalf("case %d changed result during repeated execution: %v, want %v", tc.ID, last, tc.Captures)
				}
			})
			b.Run("api", func(b *testing.B) {
				b.ReportAllocs()
				var last []int
				for b.Loop() {
					last = r.FindStringSubmatchIndex(s)
				}
				if !reflect.DeepEqual(last, tc.Captures) {
					b.Fatalf("case %d changed public API result during repeated execution", tc.ID)
				}
			})
			b.Run("call", func(b *testing.B) {
				// Same compiler, subject and host checks; isolate the old Code.Call
				// gateway against the leased entry without changing nyanjit versions.
				control := r.NewMatcher()
				args := execution.Words(control.state)
				control.entry = func(*state) uintptr {
					status, err := r.code.Call(args...)
					if err != nil {
						panic(err)
					}
					return status
				}
				b.ReportAllocs()
				var last []int
				for b.Loop() {
					var err error
					last, err = control.Find(s, 0)
					if err != nil {
						b.Fatal(err)
					}
				}
				if !reflect.DeepEqual(last, tc.Captures) {
					b.Fatalf("case %d changed Code.Call result during repeated execution", tc.ID)
				}
			})
		})
		r.Close()
	}
}
