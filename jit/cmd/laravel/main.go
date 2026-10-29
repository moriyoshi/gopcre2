package main

import (
	p "github.com/KarpelesLab/gopcre2"
	"github.com/KarpelesLab/gopcre2/internal/replay"
	"github.com/KarpelesLab/gopcre2/jit"
)

func main() {
	replay.Main(func(body string, flags uint64) (replay.MatchFunc, error) {
		r, err := jit.Compile(body, p.Flag(flags))
		if err != nil {
			return nil, err
		}
		return r.MatchPHP, nil
	})
}
