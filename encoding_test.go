package gopcre2

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestPCRE2Encoding(t *testing.T) {
	f, err := os.Open("testdata/encoding.json.gz")
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
		Flags        Flag   `json:"flags"`
		CompileError int    `json:"compile_error"`
		Captures     []int  `json:"captures"`
	}
	if err := json.NewDecoder(z).Decode(&cases); err != nil {
		t.Fatal(err)
	}
	for i, tc := range cases {
		pattern, _ := hex.DecodeString(tc.Pattern)
		subject, _ := hex.DecodeString(tc.Subject)
		r, err := Compile(string(pattern), tc.Flags)
		if tc.CompileError != 0 {
			if err == nil {
				t.Errorf("case %d: accepted invalid pattern %q, flags %v", i, pattern, tc.Flags)
			}
			continue
		}
		if err != nil {
			t.Errorf("case %d: %v", i, err)
			continue
		}
		got := r.FindStringSubmatchIndex(string(subject))
		if !reflect.DeepEqual(got, tc.Captures) {
			t.Errorf("case %d: %q @ %x, flags %v: %v, PCRE2 %v", i, pattern, subject, tc.Flags, got, tc.Captures)
		}
	}
	t.Logf("Compared %d binary-safe PCRE2 encoding fixtures", len(cases))
}

func TestEncodingIteration(t *testing.T) {
	for _, tc := range []struct {
		flags       Flag
		want        [][]int
		replacement string
	}{
		{0, [][]int{{0, 1}, {1, 2}}, "xx"},
		{UTF, [][]int{{0, 2}}, "x"},
	} {
		r := MustCompile(`.`, tc.flags)
		if got := r.FindAllStringIndex("é", -1); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("flags %v: %v", tc.flags, got)
		}
		if got := r.ReplaceAllString("é", "x"); got != tc.replacement {
			t.Errorf("flags %v: replacement %q", tc.flags, got)
		}
	}
}
