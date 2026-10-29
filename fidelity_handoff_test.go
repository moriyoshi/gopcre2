package gopcre2

import (
	"reflect"
	"testing"
)

func TestHandoffFidelity(t *testing.T) {
	for _, tc := range []struct {
		pattern, subject string
		flags            Flag
		want             []int
	}{
		{`(?<q>["'])(.*?)\k<q>`, `say "hi" now`, 0, []int{4, 8, 4, 5, 5, 7}},
		{`(?<=(a))b`, `ab`, 0, []int{1, 2, 0, 1}},
		{`^$`, "a\n", Multiline, nil},
		{`a{,2}`, `a{,2}`, 0, []int{0, 1}},
		{`(?U)<.*>`, `<aa> <bb> <cc>`, 0, []int{0, 4}},
		{`(?U:<.*>) <.*>`, `<aa> <bb> <cc>`, 0, []int{0, 14}},
		{`(?<=(a))bX|b`, `ab`, 0, []int{1, 2, -1, -1}},
		{`(?J)(?<q>a)|(?<q>b)\k<q>`, `bb`, 0, []int{0, 2, -1, -1, 0, 1}},
		{`(a)\g{-1}`, `aa`, 0, []int{0, 2, 0, 1}},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			r := MustCompile(tc.pattern, tc.flags)
			if got := r.FindStringSubmatchIndex(tc.subject); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v; PCRE2 %v", got, tc.want)
			}
		})
	}
	for _, pattern := range []string{`(?P<1>blub)`, `(?<1>blub)`, `(?'1'blub)`} {
		if _, err := Compile(pattern); err == nil {
			t.Errorf("accepted digit-led group name %q", pattern)
		}
	}
}

// Answers supplied by the php2go coordinator from PHP 8.5.11 / PCRE2 10.44.
func TestHandoffF7UTFNegativeLookbehind(t *testing.T) {
	r := MustCompile(`(?<!ක)`, UTF|UCP)
	if got := r.FindAllStringSubmatchIndex("ම", -1); !reflect.DeepEqual(got, [][]int{{0, 0}, {3, 3}}) {
		t.Fatalf("FindAll: %v", got)
	}
	if got, err := r.SubstituteAll("ම", "*"); err != nil || got != "*ම*" {
		t.Fatalf("SubstituteAll: %q, %v", got, err)
	}
}
