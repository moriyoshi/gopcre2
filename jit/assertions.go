package jit

import (
	p "github.com/KarpelesLab/gopcre2"
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

func (c *emitter) assertion(e *p.Expression, f fragment, next, fail n.Label) {
	b := c.b
	negative := e.Kind() == p.NdNegLookahead || e.Kind() == p.NdNegLookbehind
	behind := e.Kind() == p.NdLookbehind || e.Kind() == p.NdNegLookbehind
	position := c.slot()
	type savedSlot struct{ capture, scratch int }
	var saved []savedSlot
	var saveCaptures func(*p.Expression)
	saveCaptures = func(e *p.Expression) {
		if e.Kind() == p.NdCapture || e.Kind() == p.NdNamedCapture {
			saved = append(saved, savedSlot{e.Capture() * 2, c.slot()}, savedSlot{e.Capture()*2 + 1, c.slot()})
		}
		for i := 0; i < e.NumChildren(); i++ {
			saveCaptures(e.Child(i))
		}
	}
	saveCaptures(e)

	matched, missed, undo := b.NewLabel(), b.NewLabel(), b.NewLabel()
	branches := []*p.Expression{e.Child(0)}
	if behind {
		branches = lookbehindBranches(e.Child(0))
	}
	entries := make([]n.Label, len(branches))
	for i := range entries {
		entries[i] = b.NewLabel()
	}
	b.Bind(f.match)
	c.tick()
	c.store(c.scratch, position, b.Get(c.pos))
	for _, s := range saved {
		c.store(c.scratch, s.scratch, c.load(c.captures, s.capture))
	}
	b.Jump(entries[0])
	c.coldBlock(func() { b.Bind(f.back); b.Jump(undo) })
	for i, branch := range branches {
		child := c.fragment()
		branchMissed := b.NewLabel()
		b.Bind(entries[i])
		b.Set(c.pos, c.load(c.scratch, position))
		if behind {
			width, _ := fixedLookbehindWidth(branch)
			b.Branch(b.CompareImmediate(b.Get(c.pos), ir.BranchBelow, int64(width)), branchMissed)
			b.Set(c.pos, b.Sub(b.Get(c.pos), b.ConstNative(uintptr(width))))
		}
		b.Jump(child.match)
		c.emit(branch, child, matched, branchMissed)
		c.coldBlock(func() {
			b.Bind(branchMissed)
			for _, s := range saved {
				c.store(c.captures, s.capture, c.load(c.scratch, s.scratch))
			}
			if i+1 < len(entries) {
				b.Jump(entries[i+1])
			} else {
				b.Jump(missed)
			}
		})
	}

	b.Bind(matched)
	b.Set(c.pos, c.load(c.scratch, position))
	if negative {
		b.Jump(undo)
	} else {
		b.Jump(next)
	}
	c.coldBlock(func() {
		b.Bind(missed)
		b.Set(c.pos, c.load(c.scratch, position))
		for _, s := range saved {
			c.store(c.captures, s.capture, c.load(c.scratch, s.scratch))
		}
		if negative {
			b.Jump(next)
		} else {
			b.Jump(fail)
		}
		b.Bind(undo)
		b.Set(c.pos, c.load(c.scratch, position))
		for _, s := range saved {
			c.store(c.captures, s.capture, c.load(c.scratch, s.scratch))
		}
		b.Jump(fail)
	})
}

func (c *emitter) word(ch n.Value) n.Predicate {
	b := c.b
	letter := b.Or(ch, b.ConstNative(32))
	pred := b.CompareImmediate(letter, ir.BranchAboveEqual, 'a').And(b.CompareImmediate(letter, ir.BranchBelowEqual, 'z')).
		Or(b.CompareImmediate(ch, ir.BranchAboveEqual, '0').And(b.CompareImmediate(ch, ir.BranchBelowEqual, '9'))).
		Or(b.CompareImmediate(ch, ir.BranchEqual, '_'))
	if c.flags&p.UCP != 0 {
		for i := 128; i < 256; i++ {
			if !wordMember(rune(i), c.flags) {
				continue
			}
			lo := i
			for i+1 < 256 && wordMember(rune(i+1), c.flags) {
				i++
			}
			pred = pred.Or(b.CompareImmediate(ch, ir.BranchAboveEqual, int64(lo)).And(b.CompareImmediate(ch, ir.BranchBelowEqual, int64(i))))
		}
	}
	return pred
}

func (c *emitter) wordBoundary(negative bool, fail n.Label) {
	b := c.b
	pass, leftWord, leftOther := b.NewLabel(), b.NewLabel(), b.NewLabel()
	boundary, nonBoundary := pass, fail
	if negative {
		boundary, nonBoundary = fail, pass
	}
	pos := b.Get(c.pos)
	b.Branch(b.CompareImmediate(pos, ir.BranchEqual, 0), leftOther)
	b.Branch(c.word(c.charAt(b.Sub(pos, b.ConstNative(1)))), leftWord, leftOther)
	b.Bind(leftWord)
	b.Branch(b.Compare(pos, ir.BranchAboveEqual, c.end), boundary)
	b.Branch(c.word(c.charAt(pos)), nonBoundary, boundary)
	b.Bind(leftOther)
	b.Branch(b.Compare(pos, ir.BranchAboveEqual, c.end), nonBoundary)
	b.Branch(c.word(c.charAt(pos)), boundary, nonBoundary)
	b.Bind(pass)
}

func (c *emitter) backreference(e *p.Expression, f fragment, next, fail n.Label) {
	b := c.b
	index := e.Capture() * 2
	offset, length := c.slot(), c.slot()
	loop, matched := b.NewLabel(), b.NewLabel()
	b.Bind(f.match)
	c.tick()
	loadStart := func() n.Value { return c.load(c.captures, index) }
	loadEnd := func() n.Value { return c.load(c.captures, index+1) }
	if old, ok := c.openCaptures[e.Capture()]; ok {
		// Self-reference in an open repeated group refers to that group's
		// previous completed iteration, not its partly overwritten start slot.
		loadStart = func() n.Value { return c.load(c.scratch, old[0]) }
		loadEnd = func() n.Value { return c.load(c.scratch, old[1]) }
	}
	start, end := loadStart(), loadEnd()
	b.Branch(b.CompareImmediate(start, ir.BranchEqual, -1), fail)
	len := b.Sub(end, start)
	b.Branch(b.Compare(len, ir.BranchAbove, b.Sub(c.end, b.Get(c.pos))), fail)
	c.store(c.scratch, offset, b.ConstNative(0))
	c.store(c.scratch, length, len)
	b.Jump(loop)
	b.Bind(loop)
	i := c.load(c.scratch, offset)
	b.Branch(b.Compare(i, ir.BranchAboveEqual, c.load(c.scratch, length)), matched)
	c.tick()
	a := c.charAt(b.Add(loadStart(), i))
	d := c.charAt(b.Add(b.Get(c.pos), i))
	if e.Flags()&p.Caseless != 0 {
		fold := func(x n.Value) n.Value {
			upper := b.CompareImmediate(x, ir.BranchAboveEqual, 'A').And(b.CompareImmediate(x, ir.BranchBelowEqual, 'Z'))
			if e.Flags()&p.UCP != 0 {
				upper = upper.Or(b.CompareImmediate(x, ir.BranchAboveEqual, 0xc0).And(b.CompareImmediate(x, ir.BranchBelowEqual, 0xd6))).
					Or(b.CompareImmediate(x, ir.BranchAboveEqual, 0xd8).And(b.CompareImmediate(x, ir.BranchBelowEqual, 0xde)))
			}
			yes, no, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
			b.Branch(upper, yes, no)
			b.Bind(yes)
			b.JumpArgs(done, b.Add(x, b.ConstNative(32)))
			b.Bind(no)
			b.JumpArgs(done, x)
			return b.BindParams(done, 1)[0]
		}
		a, d = fold(a), fold(d)
	}
	b.Branch(b.Compare(a, ir.BranchNotEqual, d), fail)
	c.store(c.scratch, offset, b.Add(i, b.ConstNative(1)))
	b.Jump(loop)
	b.Bind(matched)
	b.Set(c.pos, b.Add(b.Get(c.pos), c.load(c.scratch, length)))
	b.Jump(next)
	c.coldBlock(func() { b.Bind(f.back); b.Set(c.pos, b.Sub(b.Get(c.pos), c.load(c.scratch, length))); b.Jump(fail) })
}
