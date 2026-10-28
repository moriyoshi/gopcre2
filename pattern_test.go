package gopcre2

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestCaptureDeclarationsInUnexecutedGroups(t *testing.T) {
	re := MustCompile(`a(?<unused>(b)){0}a`)
	if re.NumSubexp() != 2 || re.SubexpIndex("unused") != 1 {
		t.Fatalf("lost declarations: %v", re.SubexpNames())
	}
	want := []int{0, 2, -1, -1, -1, -1}
	if got := re.FindStringSubmatchIndex("aa"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Lowering repeated named groups must not register the same name per copy.
	re = MustCompile(`(?<x>a){2,3}`)
	if got := re.prog.NameToGroup["x"]; !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("duplicate declaration: %v", got)
	}
}

func TestStructuredPatternAndMinimum(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		min     int
		subject string
	}{
		{`a{2,4}b`, 3, "aaab"}, {`(?=abcd)a`, 1, "abcd"}, {`(?<=ab)c`, 1, "abc"},
		{`(?:abc|d)e`, 2, "de"}, {`a(*ACCEPT)xyz`, 0, "a"}, {`(a){0}b`, 1, "b"},
	} {
		re := MustCompile(tc.pattern)
		if got := re.Pattern().MinBytes(); got != tc.min {
			t.Errorf("%q: minimum %d, want %d", tc.pattern, got, tc.min)
		}
		if !re.MatchString(tc.subject) {
			t.Errorf("%q: false negative on %q", tc.pattern, tc.subject)
		}
	}
	re := MustCompile(`(?i:a+)b`)
	root := re.Pattern().Root()
	repeat := root.Child(0).Child(0)
	if repeat.Kind() != NdRepeat || repeat.Child(0).Flags()&Caseless == 0 || root.Child(1).Flags()&Caseless != 0 {
		t.Fatal("lost structure or option scope")
	}
}

func TestConcurrentBacktracking(t *testing.T) {
	patterns := []*Regexp{MustCompile(`a+ab`), MustCompile(`(ab|a)b`), MustCompile(`x+xy`)}
	inputs := []string{"aaaaab", "ab", "xxxxy"}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			j := i % len(patterns)
			for n := 0; n < 200; n++ {
				if !patterns[j].MatchString(inputs[j]) {
					t.Errorf("concurrent match failed: %d", j)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestEmptyRepeatProgress(t *testing.T) {
	for _, pattern := range []string{`(?:a*)*b`, `(a*)*b`, `(?:b*)++m`, `((?>a|)+?)b`} {
		re := MustCompile(pattern).SetMatchLimit(10000)
		if !re.MatchString("aaabm") {
			t.Errorf("%q failed instead of exiting an empty iteration", pattern)
		}
	}
	// A subroutine entered directly has no enclosing repeat progress slot.
	MustCompile(`(?1)(a)*+(?2)(b(?1))`).MatchString("aababa")
}

func TestInlineMultilineSearch(t *testing.T) {
	if got := MustCompile(`(?m)^abc`).FindStringIndex("--\nabc"); !reflect.DeepEqual(got, []int{3, 6}) {
		t.Fatalf("inline multiline search was incorrectly anchored: %v", got)
	}
}

func TestConsumingRepeatHeapBudget(t *testing.T) {
	// A consuming loop needs no empty-iteration checkpoint. Extra progress
	// frames must not make a previously sufficient heap budget reject input.
	if !MustCompile(`a+`).SetHeapLimit(4096).MatchString(strings.Repeat("a", 40)) {
		t.Fatal("progress guard consumed the matching heap budget")
	}
}

func BenchmarkSearch(b *testing.B) {
	for _, pattern := range []string{`AbC`, `..a.....b`, `\d+123`} {
		b.Run(pattern, func(b *testing.B) {
			re := MustCompile(pattern)
			b.ReportAllocs()
			for b.Loop() {
				re.FindStringSubmatchIndex("bbbbbbbbbbbbbbbbbbbbbabbbbbbbb123")
			}
		})
	}
}
