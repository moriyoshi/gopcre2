// Package replay loads normalized, native-validated Laravel matching units.
package replay

import (
	"bufio"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"unicode/utf8"
)

type Call struct {
	Pattern  string  `json:"pattern"`
	Body     string  `json:"body"`
	Flags    uint64  `json:"flags"`
	Subject  string  `json:"subject"`
	Offset   int     `json:"offset"`
	Global   bool    `json:"global"`
	Limit    int     `json:"limit"`
	Expected [][]int `json:"expected"`
}

type MatchFunc func(string, int, bool, bool) ([]int, error)
type CompileFunc func(string, uint64) (MatchFunc, error)
type Program struct {
	match MatchFunc
	utf   bool
}

func Load(path string) ([]Call, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	scan := bufio.NewScanner(z)
	scan.Buffer(make([]byte, 4096), 16<<20)
	var calls []Call
	for scan.Scan() {
		var c Call
		if err := json.Unmarshal(scan.Bytes(), &c); err != nil {
			return nil, err
		}
		s, err := base64.StdEncoding.DecodeString(c.Subject)
		if err != nil {
			return nil, err
		}
		c.Subject = string(s)
		calls = append(calls, c)
	}
	return calls, scan.Err()
}

func Compile(calls []Call, compile CompileFunc) (map[string]Program, error) {
	ps := make(map[string]Program)
	for _, c := range calls {
		if _, ok := ps[c.Pattern]; ok {
			continue
		}
		match, err := compile(c.Body, c.Flags)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Pattern, err)
		}
		ps[c.Pattern] = Program{match, c.Flags&512 != 0}
	}
	return ps, nil
}

func Run(ps map[string]Program, c Call) ([][]int, error) {
	p := ps[c.Pattern]
	offset, notEmpty := c.Offset, false
	if offset < 0 {
		offset += len(c.Subject)
	}
	limit := c.Limit
	if !c.Global {
		limit = 1
	}
	var out [][]int
	for limit != 0 {
		ov, err := p.match(c.Subject, offset, notEmpty, notEmpty)
		if err != nil {
			return nil, err
		}
		if ov == nil {
			if notEmpty && offset < len(c.Subject) {
				size := 1
				if p.utf {
					_, size = utf8.DecodeRuneInString(c.Subject[offset:])
				}
				offset += size
				notEmpty = false
				continue
			}
			break
		}
		n := len(ov)
		for n > 2 && ov[n-1] < 0 && ov[n-2] < 0 {
			n -= 2
		}
		out = append(out, append([]int(nil), ov[:n]...))
		if !c.Global {
			break
		}
		if limit > 0 {
			limit--
		}
		offset = ov[1]
		notEmpty = ov[0] == ov[1]
	}
	return out, nil
}

func Validate(ps map[string]Program, calls []Call) error {
	for i, c := range calls {
		got, err := Run(ps, c)
		if err != nil || !reflect.DeepEqual(got, c.Expected) {
			return fmt.Errorf("case %d %s: %v %v; native %v", i, c.Pattern, got, err, c.Expected)
		}
	}
	return nil
}
