package jit

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	p "github.com/KarpelesLab/gopcre2"
)

func TestNativePCRE2Encoding(t *testing.T) {
	// Use native's normal platform guard before reading the fixture.
	native(t, "a")
	f, err := os.Open("../testdata/encoding.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var cases []struct {
		Pattern      string `json:"pattern_hex"`
		Subject      string `json:"subject_hex"`
		Flags        p.Flag `json:"flags"`
		CompileError int    `json:"compile_error"`
		Captures     []int  `json:"captures"`
	}
	if err := json.NewDecoder(z).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	type key struct {
		pattern string
		flags   p.Flag
	}
	matchers := make(map[key]*Matcher)
	compared, subjectFallback, patternFallback := 0, 0, 0
	for i, tc := range cases {
		if tc.CompileError != 0 {
			continue
		} // the base compiler checks syntax
		pattern, _ := hex.DecodeString(tc.Pattern)
		subject, _ := hex.DecodeString(tc.Subject)
		k := key{string(pattern), tc.Flags}
		m := matchers[k]
		if m == nil {
			r, err := Compile(k.pattern, k.flags)
			if err != nil {
				t.Fatalf("case %d: %v", i, err)
			}
			t.Cleanup(func() { r.Close() })
			m = r.NewMatcher()
			matchers[k] = m
		}
		if !m.re.Native() {
			patternFallback++
			continue
		}
		for repeat := 0; repeat < 3; repeat++ {
			got, err := m.Find(string(subject), 0)
			if errors.Is(err, ErrSubject) {
				subjectFallback++
				break
			}
			if err != nil || !reflect.DeepEqual(got, tc.Captures) {
				t.Errorf("case %d: %q @ %x, flags %v: %v, %v; PCRE2 %v", i, pattern, subject, tc.Flags, got, err, tc.Captures)
				break
			}
			if repeat == 0 {
				compared++
			}
		}
	}
	t.Logf("Encoding fixtures: %d native comparisons, %d subject fallbacks, %d pattern fallbacks", compared, subjectFallback, patternFallback)
	if compared < 12000 {
		t.Fatalf("byte-mode coverage fell: %d", compared)
	}
}
