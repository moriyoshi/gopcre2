package gopcre2

import (
	"errors"
	"reflect"
	"testing"

	"github.com/KarpelesLab/gopcre2/internal/replay"
)

func TestLaravelNativeCaptures(t *testing.T) {
	for _, route := range []string{"eloquent", "json"} {
		t.Run(route, func(t *testing.T) {
			calls, err := replay.Load("benchmarks/laravel/testdata/" + route + ".jsonl.gz")
			if err != nil {
				t.Fatal(err)
			}
			ps, err := replay.Compile(calls, func(body string, flags uint64) (replay.MatchFunc, error) {
				r, err := Compile(body, Flag(flags))
				if err != nil {
					return nil, err
				}
				return r.MatchPHP, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := replay.Validate(ps, calls); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMatchPHPOptionsAndGlobalRetry(t *testing.T) {
	r := MustCompile(`(?:|a)`)
	if got, err := r.MatchPHP("a", 0, true, true); err != nil || !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("nonempty retry: %v %v", got, err)
	}
	if got := r.FindAllStringSubmatchIndex("a", -1); !reflect.DeepEqual(got, [][]int{{0, 0}, {0, 1}, {1, 1}}) {
		t.Fatalf("global retry: %v", got)
	}
	if got, err := r.SubstituteAll("a", "*"); err != nil || got != "***" {
		t.Fatalf("replacement retry: %q %v", got, err)
	}
	if got, err := MustCompile(`(?=$)`).SubstituteAll("abc", "*"); err != nil || got != "abc*" {
		t.Fatalf("final empty replacement: %q %v", got, err)
	}
	if got, err := MustCompile(`(?<=a)b`).MatchPHP("ab", 1, true, false); err != nil || !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("lookbehind at offset: %v %v", got, err)
	}
	if got, err := MustCompile(`a`).MatchPHP("ba", 0, true, false); err != nil || got != nil {
		t.Fatalf("anchored search: %v %v", got, err)
	}
}

func TestLookaroundReuseAndLimits(t *testing.T) {
	for _, pattern := range []string{`(?<=a)b`, `a(?=b)`, `(?>a)b`, `(?=(?<=a)(b))b`} {
		r := MustCompile(pattern)
		m := r.NewMatcher()
		if n := testing.AllocsPerRun(100, func() {
			got, err := m.Find("ab", 0, false, false)
			if err != nil || got == nil {
				panic("match failed")
			}
		}); n != 0 {
			t.Errorf("%s: %g allocations", pattern, n)
		}
		if m.vm.subject != "" || m.vm.child != nil && m.vm.child.subject != "" {
			t.Fatal("retained subject")
		}
	}
	for _, pattern := range []string{`(?=a)a`, `(?!a)b`, `(?>a)b`, `(?<=a)b`} {
		r := MustCompile(pattern).SetMatchLimit(2)
		if _, err := r.MatchPHP("ab", 0, false, false); !errors.Is(err, ErrMatchLimit) {
			t.Errorf("%s: lost child limit: %v", pattern, err)
		}
	}
	r := MustCompile(`(?=a)a`)
	r.MatchPHP("a", 0, false, false)
	r.SetMatchLimit(1)
	if _, err := r.MatchPHP("a", 0, false, false); !errors.Is(err, ErrMatchLimit) {
		t.Fatalf("stale pooled limit: %v", err)
	}
	r.SetMatchLimit(1000)
	if got, err := r.MatchPHP("a", 0, false, false); err != nil || got == nil {
		t.Fatalf("reset pooled limit: %v %v", got, err)
	}
	for _, pattern := range []string{`(?=(a))aX|a`, `(?>(a))X|a`} {
		if got := MustCompile(pattern).FindStringSubmatchIndex("a"); !reflect.DeepEqual(got, []int{0, 1, -1, -1}) {
			t.Errorf("capture undo %s: %v", pattern, got)
		}
	}
}

func TestPooledCalloutReentry(t *testing.T) {
	r := MustCompile(`(?C)a`)
	inside := false
	r.SetCallout(func(*CalloutBlock) int {
		if inside {
			return 0
		}
		inside = true
		got, err := r.MatchPHP("a", 0, false, false)
		inside = false
		if err != nil || got == nil {
			t.Fatal("reentry failed")
		}
		return 0
	})
	if got, err := r.MatchPHP("a", 0, false, false); err != nil || got == nil {
		t.Fatalf("outer call: %v %v", got, err)
	}
}

func TestLegacyCandidateLimitsAndStrictConsumer(t *testing.T) {
	r := MustCompile(`a+X|b`).SetMatchLimit(8)
	// Legacy index APIs retry subsequent candidates after a failed attempt.
	if got := r.FindStringSubmatchIndex("aaaaaaaaaacb"); !reflect.DeepEqual(got, []int{11, 12}) {
		t.Fatalf("legacy result: %v", got)
	}
	// The consumer entry point reports the exhausted attempt instead.
	if _, err := r.MatchPHP("aaaaaaaaaacb", 0, false, false); !errors.Is(err, ErrMatchLimit) {
		t.Fatalf("strict result: %v", err)
	}
}

func TestNegativeLengthMatchIsAnError(t *testing.T) {
	r := MustCompile(`(?=ab\K)a`)
	if _, err := r.MatchPHP("ab", 0, false, false); !errors.Is(err, ErrInvalidMatch) {
		t.Fatalf("negative length: %v", err)
	}
}
