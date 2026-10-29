package jit

import (
	"errors"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	p "github.com/KarpelesLab/gopcre2"
	"github.com/moriyoshi/nyanjit/backend/execution"
)

func native(t testing.TB, pattern string, flags ...p.Flag) *Regexp {
	t.Helper()
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		t.Skip("native test requires Linux amd64 or arm64")
	}
	r, err := Compile(pattern, flags...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if !r.Native() {
		t.Fatalf("%q did not compile natively: %v", pattern, r.NativeError())
	}
	return r
}

func TestNativeMatchingAndBacktracking(t *testing.T) {
	for _, tc := range []struct {
		pattern, input string
		want           []int
	}{
		{`abc`, "--abc", []int{2, 5}}, {`a+ab`, "aaaaab", []int{0, 6}},
		{`a+?a`, "aaaa", []int{0, 2}}, {`a++ab`, "aaaaab", nil},
		{`a{2,4}ab`, "aaaaab", []int{0, 6}}, {`a{2,4}?a`, "aaaaab", []int{0, 3}},
		{`(ab|a)b`, "ab", []int{0, 2, 0, 1}}, {`(a|ab)c`, "abc", []int{0, 3, 0, 2}},
		{`(a)|(bc)`, "bc", []int{0, 2, -1, -1, 0, 2}},
		{`((ab|a)b)c`, "abc", []int{0, 3, 0, 2, 0, 1}},
		{`(?>ab|a)b`, "ab", nil}, {`(?>(a)|(ab))c|abc`, "abc", []int{0, 3, -1, -1, -1, -1}},
		{`(?<unused>a){0}b`, "b", []int{0, 1, -1, -1}},
		{`[^a-c]+`, "abXYZ", []int{2, 5}}, {`\d+123`, "987654321,01234", []int{10, 14}},
		{`(?i:ab)c`, "ABc", []int{0, 3}}, {`(?i:ab)c`, "ABC", nil},
		{`\Aa*\z`, "", []int{0, 0}}, {`a?b`, "b", []int{0, 1}},
		{`a|`, "", []int{0, 0}},
	} {
		t.Run(tc.pattern+"/"+tc.input, func(t *testing.T) {
			r := native(t, tc.pattern)
			got, err := r.NewMatcher().Find(tc.input, 0)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestNativeLimitAndLifecycle(t *testing.T) {
	r := native(t, `a+a+a+b`)
	m := r.NewMatcher()
	r.SetMatchLimit(10)
	if _, err := m.Find(strings.Repeat("a", 30), 0); !errors.Is(err, p.ErrMatchLimit) {
		t.Fatalf("limit: %v", err)
	}
	r.SetMatchLimit(0)
	r.SetHeapLimit(1)
	if _, err := m.Find("aaab", 0); !errors.Is(err, p.ErrHeapLimit) {
		t.Fatalf("heap: %v", err)
	}
	r.SetHeapLimit(0)
	for i := 0; i < 10; i++ {
		runtime.GC()
		if got, err := m.Find("aaab", 0); err != nil || got == nil {
			t.Fatalf("GC: %v %v", got, err)
		}
	}
	r.Close()
	if _, err := m.Find("", 0); !errors.Is(err, execution.ErrClosed) {
		t.Fatalf("close: %v", err)
	}
}

func TestNativeAssertionsAndBackreferences(t *testing.T) {
	for _, tc := range []struct {
		pattern, input string
		want           []int
	}{
		{`(a(?:\1|)a){3}b`, "aaaaaaaaaaab", []int{3, 12, 9, 11}},
		{`(?!(a))b`, "b", []int{0, 1, -1, -1}},
		{`(?<=(ab))c`, "abc", []int{2, 3, 0, 2}},
		{`(?<!ab)c`, "xc", []int{1, 2}},
		{`\bcat\b`, "a cat!", []int{2, 5}},
	} {
		r := native(t, tc.pattern)
		got, err := r.NewMatcher().Find(tc.input, 0)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: got %v %v, want %v", tc.pattern, got, err, tc.want)
		}
	}
}

func TestNativeFallbackAndConcurrentUse(t *testing.T) {
	r, err := Compile(`\p{L}{2}`)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Native() || !errors.Is(r.NativeError(), ErrUnsupported) || !r.MatchString("aa") {
		t.Fatal("unsupported pattern fallback failed")
	}
	r = native(t, `(ab|a)b`)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				got := r.FindStringSubmatchIndex("ab")
				if !reflect.DeepEqual(got, []int{0, 2, 0, 1}) {
					t.Errorf("concurrent result %v", got)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got, err := r.NewMatcher().Find("éab", 0); err != nil || !reflect.DeepEqual(got, []int{2, 4, 2, 3}) {
		t.Fatalf("byte subject: %v, %v", got, err)
	}
	r = native(t, `(ab|a)b`, p.UTF)
	if _, err := r.NewMatcher().Find("éab", 0); !errors.Is(err, ErrSubject) {
		t.Fatalf("subject fallback: %v", err)
	}
	if !r.MatchString("éab") {
		t.Fatal("Unicode fallback failed")
	}
}

func TestNativeZeroAllocationReuse(t *testing.T) {
	r := native(t, `(ab|a)b`)
	m := r.NewMatcher()
	if n := testing.AllocsPerRun(100, func() {
		got, err := m.Find("ab", 0)
		if err != nil || got == nil {
			panic("match failed")
		}
	}); n != 0 {
		t.Fatalf("warm matching allocates: %g", n)
	}
}

func TestNativeRegularSubset(t *testing.T) {
	patterns := []string{`(a|ab)b`, `(a|b){0,3}a`, `(a|b){0,3}?a`, `((a)|b){2}`, `(a?){0,3}a`,
		`(ab)*a`, `((a)b)+b`, `(a|)b`, `a*a?a+`, `(a*)a`, `((a*)|b)c`, `.a.b`, `(ab|a){2,3}b`,
		`(a?){2,3}?b`, `(?i:a)b`, `(a{0})b`}
	subjects := []string{""}
	for depth := 0; depth < 6; depth++ {
		var next []string
		for _, s := range subjects {
			if len(s) == depth {
				next = append(next, s+"a", s+"b")
			}
		}
		subjects = append(subjects, next...)
	}
	for _, pattern := range patterns {
		r := native(t, pattern)
		m := r.NewMatcher()
		reference := regexp.MustCompile(pattern)
		for _, s := range subjects {
			want := reference.FindStringSubmatchIndex(s)
			got, err := m.Find(s, 0)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("%q @ %q: got %v %v, want %v", pattern, s, got, err, want)
			}
		}
	}
}

func TestNativeSearchStart(t *testing.T) {
	r := native(t, `\Ga`)
	m := r.NewMatcher()
	if got, err := m.Find("baa", 1); err != nil || !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("start offset: %v %v", got, err)
	}
	if got, err := m.Find("baa", 0); err != nil || got != nil {
		t.Fatalf("G anchor retried at the wrong start: %v %v", got, err)
	}
	r = native(t, `(?m)^a`)
	if got := r.FindStringSubmatchIndex("b\na"); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Fatalf("multiline: %v", got)
	}
}

func TestNativeBorrowedInputLifetime(t *testing.T) {
	r, err := Compile(`(a+)b`)
	if err != nil || !r.Native() {
		t.Fatalf("native compile: %v", err)
	}
	m := r.NewMatcher()
	r = nil // the matcher must retain the executable owner on its own
	defer func() { m.re.Close() }()
	for n := 1; n <= 512; n *= 2 {
		input := strings.Repeat("a", n) + "b"
		runtime.GC()
		got, err := m.Find(input, 0)
		if err != nil || !reflect.DeepEqual(got, []int{0, n + 1, 0, n}) {
			t.Fatalf("borrowed input after GC: %v %v", got, err)
		}
		if input != strings.Repeat("a", n)+"b" {
			t.Fatal("native code changed string storage")
		}
	}
}

func BenchmarkNative(b *testing.B) {
	for _, pattern := range []string{`AbC`, `..a.....b`, `\d+123`, `(ab|a)b`} {
		b.Run(pattern, func(b *testing.B) {
			r := native(b, pattern)
			m := r.NewMatcher()
			b.ReportAllocs()
			for b.Loop() {
				_, err := m.Find("bbbbbbbbbbbbbbbbbbbbbabbbbbbbb123", 0)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
