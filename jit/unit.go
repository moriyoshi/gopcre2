package jit

import (
	p "github.com/KarpelesLab/gopcre2"
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

type captureSpan struct{ index, start, end int }

// A fixed-width deterministic unit needs only its range bounds for retries.
// Captures within it are offsets into the last completed unit, so no frame per
// character or iteration is necessary. This includes (ab)+ and (?>a)*.
func fixedUnit(e *p.Expression) ([]*p.Expression, []captureSpan, bool) {
	var chars []*p.Expression
	var captures []captureSpan
	var walk func(*p.Expression) bool
	walk = func(e *p.Expression) bool {
		switch e.Kind() {
		case p.NdRepeat:
			min, max := e.Bounds()
			if min != max || min < 0 || min > 512 {
				return false
			}
			for i := 0; i < min; i++ {
				if !walk(e.Child(0)) {
					return false
				}
			}
			return true
		case p.NdLiteral, p.NdDot, p.NdCharClass, p.NdCharType:
			if len(chars) >= 512 {
				return false
			}
			chars = append(chars, e)
			return true
		case p.NdEmpty:
			return true
		case p.NdConcat:
			for i := 0; i < e.NumChildren(); i++ {
				if !walk(e.Child(i)) {
					return false
				}
			}
			return true
		case p.NdCapture, p.NdNamedCapture:
			start := len(chars)
			if !walk(e.Child(0)) {
				return false
			}
			captures = append(captures, captureSpan{e.Capture(), start, len(chars)})
			return true
		case p.NdNonCapture, p.NdAtomicGroup, p.NdInlineOption:
			return e.NumChildren() == 1 && walk(e.Child(0))
		}
		return false
	}
	ok := walk(e) && len(chars) > 0
	return chars, captures, ok
}

func (c *emitter) consumeUnit(chars []*p.Expression, next, fail n.Label) {
	b := c.b
	partial := make([]n.Label, len(chars))
	for i, e := range chars {
		partial[i] = b.NewLabel()
		c.checkCharacter(e, partial[i])
		b.Set(c.pos, b.Add(b.Get(c.pos), b.ConstNative(1)))
	}
	b.Jump(next)
	for i, label := range partial {
		c.coldBlock(func() {
			b.Bind(label)
			if i > 0 {
				b.Set(c.pos, b.Sub(b.Get(c.pos), b.ConstNative(uintptr(i))))
			}
			b.Jump(fail)
		})
	}
}

func (c *emitter) bounded(e *p.Expression, f fragment, next, fail n.Label) {
	b := c.b
	min, max := e.Bounds()
	parts := make([]fragment, max)
	entries := make([]n.Label, max)
	active := c.slot()
	for i := range parts {
		parts[i] = c.fragment()
		entries[i] = b.NewLabel()
	}
	b.Bind(f.match)
	b.Jump(entries[0])
	c.coldBlock(func() {
		b.Bind(f.back)
		for i, part := range parts {
			b.Branch(b.CompareImmediate(c.load(c.scratch, active), ir.BranchEqual, int64(i)), part.back)
		}
		b.Jump(fail)
	})
	for i, part := range parts {
		b.Bind(entries[i])
		c.store(c.scratch, active, b.ConstNative(uintptr(i)))
		b.Jump(part.match)
		nxt, prev := next, fail
		if i+1 < max {
			nxt = entries[i+1]
		}
		if i > 0 {
			prev = parts[i-1].back
		}
		if i < min {
			c.emit(e.Child(0), part, nxt, prev)
			continue
		}
		child := c.fragment()
		choice := c.slot()
		empty := b.NewLabel()
		b.Bind(part.match)
		if e.Greediness() == p.Lazy {
			b.Jump(empty)
		} else {
			c.store(c.scratch, choice, b.ConstNative(1))
			b.Jump(child.match)
		}
		c.coldBlock(func() {
			b.Bind(part.back)
			b.Branch(b.CompareImmediate(c.load(c.scratch, choice), ir.BranchEqual, 1), child.back)
			if e.Greediness() == p.Lazy {
				c.store(c.scratch, choice, b.ConstNative(1))
				b.Jump(child.match)
			} else {
				b.Jump(prev)
			}
		})
		childFail := empty
		if e.Greediness() == p.Lazy {
			childFail = prev
		}
		c.emit(e.Child(0), child, nxt, childFail)
		b.Bind(empty)
		c.store(c.scratch, choice, b.ConstNative(0))
		// Stopping a repeat skips all remaining optional copies. Exploring
		// independent skips would revisit identical iteration counts 2^n times.
		b.Jump(next)
	}
}

func fixedLookbehindWidth(e *p.Expression) (int, bool) {
	switch e.Kind() {
	case p.NdEmpty, p.NdAnchorBeginText, p.NdAnchorEndText, p.NdAnchorBeginLine, p.NdAnchorEndLine, p.NdAnchorEndTextOpt, p.NdAnchorStartOfMatch, p.NdWordBoundary, p.NdNonWordBoundary, p.NdLookahead, p.NdNegLookahead, p.NdLookbehind, p.NdNegLookbehind:
		return 0, true
	case p.NdLiteral, p.NdDot, p.NdCharClass, p.NdCharType:
		return 1, true
	case p.NdCapture, p.NdNamedCapture, p.NdNonCapture, p.NdAtomicGroup, p.NdInlineOption:
		if e.NumChildren() == 0 {
			return 0, true
		}
		return fixedLookbehindWidth(e.Child(0))
	case p.NdConcat:
		total := 0
		for i := 0; i < e.NumChildren(); i++ {
			width, ok := fixedLookbehindWidth(e.Child(i))
			if !ok || total+width > 512 {
				return 0, false
			}
			total += width
		}
		return total, true
	case p.NdRepeat:
		min, max := e.Bounds()
		if min < 0 || min != max || min > 512 {
			return 0, false
		}
		if min == 0 {
			return 0, true
		}
		width, ok := fixedLookbehindWidth(e.Child(0))
		return min * width, ok && min*width <= 512
	case p.NdAlternate:
		width := -1
		for i := 0; i < e.NumChildren(); i++ {
			n, ok := fixedLookbehindWidth(e.Child(i))
			if !ok || width >= 0 && width != n {
				return 0, false
			}
			width = n
		}
		return width, width >= 0
	}
	return 0, false
}
func lookbehindBranches(e *p.Expression) []*p.Expression {
	if e.Kind() == p.NdAlternate {
		branches := make([]*p.Expression, e.NumChildren())
		for i := range branches {
			branches[i] = e.Child(i)
		}
		return branches
	}
	return []*p.Expression{e}
}
