package gopcre2

import (
	"reflect"
	"testing"
)

func TestStartProofs(t *testing.T) {
	for _, tc := range []struct {
		pattern         string
		flags           Flag
		bytes           string
		known, anchored bool
	}{
		{`(?<=^|;)get([^;]+?)Attribute(;|$)`, 0, "g", true, false},
		{`(?:\Afoo|\Abar)`, 0, "bf", true, true},
		{`\Gfoo`, 0, "f", true, true},
		{`(?m)^foo`, 0, "f", true, false},
		{`a?b`, 0, "ab", true, false},
		{`a*`, 0, "", false, false},
		{`.a`, 0, "", false, false},
		{`(*ACCEPT)z`, 0, "", false, false},
	} {
		r := MustCompile(tc.pattern, tc.flags)
		set, known := r.Pattern().Root().StartBytes()
		if known != tc.known || r.Pattern().AnchoredAtStart() != tc.anchored {
			t.Errorf("%s: known=%v anchored=%v", tc.pattern, known, r.Pattern().AnchoredAtStart())
			continue
		}
		if known {
			var got string
			for i := 0; i < 256; i++ {
				if byteIn(set, byte(i)) {
					got += string(byte(i))
				}
			}
			if got != tc.bytes {
				t.Errorf("%s: FIRST %q", tc.pattern, got)
			}
		}
	}
}

func TestStartFiltersPreserveCaptures(t *testing.T) {
	for _, pattern := range []string{`(?<=^|;)get([^;]+?)Attribute(;|$)`, `(?:\Afoo|bar)`, `a?b`, `(a|b|c)+`, `(?i)[a-c]+`, `(?i)k+`, `[\W]+`, `\bfoo|\bbar`, `(?=(a))aX|a`, `(*ACCEPT)z`} {
		for _, flags := range []Flag{0, UTF | UCP} {
			a := MustCompile(pattern, flags)
			b := MustCompile(pattern, flags|NoStartOptimize)
			for _, subject := range []string{"", "a", "ba", "xxbar", "foo;getNameAttribute;bar", "xxKkk", "é abc"} {
				if got, want := a.FindStringSubmatchIndex(subject), b.FindStringSubmatchIndex(subject); !reflect.DeepEqual(got, want) {
					t.Errorf("%s %q flags %v: %v without filters %v", pattern, subject, flags, got, want)
				}
			}
		}
	}
}
