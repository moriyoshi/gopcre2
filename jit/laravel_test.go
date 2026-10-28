package jit

import (
	"reflect"
	"testing"

	p "github.com/KarpelesLab/gopcre2"
	"github.com/KarpelesLab/gopcre2/internal/replay"
)

func TestNativeRankedLaravelGaps(t *testing.T) {
	for _, tc := range []struct {
		pattern, subject string
		want             []int
	}{
		{`(?<=^|;)get([^;]+?)Attribute(;|$)`, `foo;getNameAttribute;bar`, []int{4, 21, 7, 11, 20, 21}},
		{`(?<=^|;)get([^;]+?)Attribute(;|$)`, `getNameAttribute`, []int{0, 16, 3, 7, 16, 16}},
		{`(?<=(a)|(ba))X`, `baX`, []int{2, 3, 1, 2, -1, -1}},
		{`(?<!ab|c)d`, `ad`, []int{1, 2}},
		{`(?<!ab|c)d`, `cd`, nil},
		{`(?<!\\)((?:\\{2})*)c`, `\\\\c`, []int{0, 5, 0, 4}},
		{`(?<!\\)((?:\\{2})*)c`, `\\c`, []int{0, 3, 0, 2}},
		{`(?<!\\)((?:\\{2})*)c`, `\c`, nil},
		{`(?:(a){2})+`, `aaaa`, []int{0, 4, 3, 4}},
		{`(?:(a){2})+X|aaaa`, `aaaa`, []int{0, 4, -1, -1}},
	} {
		t.Run(tc.pattern+tc.subject, func(t *testing.T) {
			r := native(t, tc.pattern)
			got, err := r.NewMatcher().Find(tc.subject, 0)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%v %v; native PCRE2 %v", got, err, tc.want)
			}
		})
	}
	r := native(t, `(?:|a)`)
	if got, err := r.NewMatcher().FindWithOptions("a", 0, true, true); err != nil || !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("empty retry: %v %v", got, err)
	}
	if got, err := native(t, `a`).MatchPHP("ba", 0, true, false); err != nil || got != nil {
		t.Fatalf("anchored: %v %v", got, err)
	}
	r = native(t, `[A-Za-z0-9\x{0080}-\x{FFFF}]$`, p.UTF|p.UCP)
	if got, err := r.MatchPHP("é", 0, false, false); err != nil || !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("UTF fallback: %v %v", got, err)
	}
}

func TestLaravelNativeCaptures(t *testing.T) {
	native(t, "a")
	for _, route := range []string{"eloquent", "json"} {
		t.Run(route, func(t *testing.T) {
			calls, err := replay.Load("../benchmarks/laravel/testdata/" + route + ".jsonl.gz")
			if err != nil {
				t.Fatal(err)
			}
			compiled := make(map[string]bool)
			ps, err := replay.Compile(calls, func(body string, flags uint64) (replay.MatchFunc, error) {
				r, err := Compile(body, p.Flag(flags))
				if err != nil {
					return nil, err
				}
				compiled[body] = r.Native()
				t.Cleanup(func() { r.Close() })
				return r.MatchPHP, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := replay.Validate(ps, calls); err != nil {
				t.Fatal(err)
			}
			covered := 0
			for _, c := range calls {
				if compiled[c.Body] {
					covered++
				}
			}
			t.Logf("%s: %d/%d calls have native code; every invocation agrees with native PHP captures", route, covered, len(calls))
		})
	}
}
