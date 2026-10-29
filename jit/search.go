package jit

import (
	p "github.com/KarpelesLab/gopcre2"
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// A required literal at a proven fixed offset is a cheap candidate filter.
// Do not look through variable consuming expressions or use assertion bodies
// as though they consumed input. No pattern or repeat strings are expanded.
func firstLiteral(e *p.Expression) (rune, p.Flag, int, bool, bool) {
	switch e.Kind() {
	case p.NdLiteral:
		return e.Rune(), e.Flags(), 0, true, true
	case p.NdDot, p.NdCharClass, p.NdCharType:
		return 0, 0, 1, false, true
	case p.NdCapture, p.NdNamedCapture, p.NdNonCapture, p.NdAtomicGroup:
		return firstLiteral(e.Child(0))
	case p.NdInlineOption:
		if e.NumChildren() > 0 {
			return firstLiteral(e.Child(0))
		}
		return 0, 0, 0, false, true
	case p.NdConcat:
		offset := 0
		for i := 0; i < e.NumChildren(); i++ {
			r, flags, n, found, fixed := firstLiteral(e.Child(i))
			if found {
				return r, flags, offset + n, true, false
			}
			if !fixed {
				return 0, 0, 0, false, false
			}
			offset += n
			if offset > 8 {
				return 0, 0, 0, false, false
			}
		}
		return 0, 0, offset, false, true
	case p.NdRepeat:
		min, max := e.Bounds()
		if max == 0 {
			return 0, 0, 0, false, true
		}
		if min == 0 {
			return 0, 0, 0, false, false
		}
		r, flags, n, found, fixed := firstLiteral(e.Child(0))
		if found {
			return r, flags, n, true, false
		}
		if fixed && min == max && n <= 8 && min <= 8 {
			return 0, 0, n * min, false, true
		}
	case p.NdEmpty, p.NdAnchorBeginText, p.NdAnchorBeginLine, p.NdAnchorEndLine, p.NdAnchorEndText,
		p.NdAnchorEndTextOpt, p.NdAnchorStartOfMatch, p.NdWordBoundary, p.NdNonWordBoundary,
		p.NdLookahead, p.NdNegLookahead, p.NdLookbehind, p.NdNegLookbehind:
		return 0, 0, 0, false, true
	}
	return 0, 0, 0, false, false
}

func (c *emitter) checkStartSet(e *p.Expression, pos n.Value, fail n.Label) {
	set, known := e.StartBytes()
	if !known {
		return
	}
	b := c.b
	b.Branch(b.Compare(pos, ir.BranchAboveEqual, c.end), fail)
	ch := c.charAt(pos)
	predicate := b.CompareImmediate(ch, ir.BranchBelow, 0)
	contains := func(i int) bool { return set[i>>6]&(uint64(1)<<uint(i&63)) != 0 }
	for i := 0; i < 256; i++ {
		if !contains(i) {
			continue
		}
		lo := i
		for i+1 < 256 && contains(i+1) {
			i++
		}
		part := b.CompareImmediate(ch, ir.BranchEqual, int64(lo))
		if lo != i {
			part = b.CompareImmediate(ch, ir.BranchAboveEqual, int64(lo)).And(b.CompareImmediate(ch, ir.BranchBelowEqual, int64(i)))
		}
		predicate = predicate.Or(part)
	}
	b.Branch(predicate.Not(), fail)
}
