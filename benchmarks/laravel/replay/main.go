package main

import (
	p "github.com/KarpelesLab/gopcre2"
	"github.com/KarpelesLab/gopcre2/internal/replay"
)

func main() {
	replay.Main(func(body string, flags uint64) (replay.MatchFunc, error) {
		r, err := p.Compile(body, p.Flag(flags))
		if err != nil {
			return nil, err
		}
		return r.MatchPHP, nil
	})
}
