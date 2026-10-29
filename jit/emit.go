package jit

import (
	p "github.com/KarpelesLab/gopcre2"
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// Every fragment has two native entries. match attempts the expression;
// back reverses its last success and tries its next result. Exhaustion restores
// the input/captures and branches to fail. There is no runtime opcode loop.
func (c *emitter) emit(e *p.Expression, f fragment, next, fail n.Label) {
	b := c.b
	switch e.Kind() {
	case p.NdConcat:
		children := make([]fragment, e.NumChildren())
		for i := range children {
			children[i] = c.fragment()
		}
		b.Bind(f.match)
		if len(children) == 0 {
			b.Jump(next)
		} else {
			b.Jump(children[0].match)
		}
		c.coldBlock(func() {
			b.Bind(f.back)
			if len(children) == 0 {
				b.Jump(fail)
			} else {
				b.Jump(children[len(children)-1].back)
			}
		})
		for i, ch := range children {
			nxt, prev := next, fail
			if i+1 < len(children) {
				nxt = children[i+1].match
			}
			if i > 0 {
				prev = children[i-1].back
			}
			c.emit(e.Child(i), ch, nxt, prev)
		}
	case p.NdAlternate:
		choice := c.slot()
		children := make([]fragment, e.NumChildren())
		entries := make([]n.Label, len(children))
		for i := range children {
			children[i] = c.fragment()
			entries[i] = b.NewLabel()
		}
		b.Bind(f.match)
		b.Jump(entries[0])
		c.coldBlock(func() {
			b.Bind(f.back)
			for i, ch := range children {
				b.Branch(b.CompareImmediate(c.load(c.scratch, choice), ir.BranchEqual, int64(i)), ch.back)
			}
			b.Jump(fail)
		})
		for i, ch := range children {
			b.Bind(entries[i])
			if c.flags&p.NoStartOptimize == 0 {
				skip := fail
				if i+1 < len(entries) {
					skip = entries[i+1]
				}
				c.checkStartSet(e.Child(i), b.Get(c.pos), skip)
			}
			c.store(c.scratch, choice, b.ConstNative(uintptr(i)))
			b.Jump(ch.match)
			prev := fail
			if i+1 < len(children) {
				prev = entries[i+1]
			}
			c.emit(e.Child(i), ch, next, prev)
		}
	case p.NdCapture, p.NdNamedCapture:
		s0, s1 := c.slot(), c.slot()
		idx := e.Capture() * 2
		child := c.fragment()
		out, undo := b.NewLabel(), b.NewLabel()
		b.Bind(f.match)
		c.store(c.scratch, s0, c.load(c.captures, idx))
		c.store(c.scratch, s1, c.load(c.captures, idx+1))
		c.store(c.captures, idx, b.Get(c.pos))
		b.Jump(child.match)
		c.coldBlock(func() { b.Bind(f.back); b.Jump(child.back) })
		if c.openCaptures == nil {
			c.openCaptures = make(map[int][2]int)
		}
		old, exists := c.openCaptures[e.Capture()]
		c.openCaptures[e.Capture()] = [2]int{s0, s1}
		c.emit(e.Child(0), child, out, undo)
		if exists {
			c.openCaptures[e.Capture()] = old
		} else {
			delete(c.openCaptures, e.Capture())
		}
		b.Bind(out)
		c.store(c.captures, idx+1, b.Get(c.pos))
		b.Jump(next)
		c.coldBlock(func() {
			b.Bind(undo)
			c.store(c.captures, idx, c.load(c.scratch, s0))
			c.store(c.captures, idx+1, c.load(c.scratch, s1))
			b.Jump(fail)
		})
	case p.NdAtomicGroup:
		position := c.slot()
		type savedSlot struct{ capture, scratch int }
		var saved []savedSlot
		var captures func(*p.Expression)
		captures = func(e *p.Expression) {
			if e.Kind() == p.NdCapture || e.Kind() == p.NdNamedCapture {
				saved = append(saved, savedSlot{e.Capture() * 2, c.slot()}, savedSlot{e.Capture()*2 + 1, c.slot()})
			}
			for i := 0; i < e.NumChildren(); i++ {
				captures(e.Child(i))
			}
		}
		captures(e)
		child := c.fragment()
		undo := b.NewLabel()
		b.Bind(f.match)
		c.store(c.scratch, position, b.Get(c.pos))
		for _, s := range saved {
			c.store(c.scratch, s.scratch, c.load(c.captures, s.capture))
		}
		b.Jump(child.match)
		c.coldBlock(func() { b.Bind(f.back); b.Jump(undo) })
		c.emit(e.Child(0), child, next, undo)
		c.coldBlock(func() {
			b.Bind(undo)
			b.Set(c.pos, c.load(c.scratch, position))
			for _, s := range saved {
				c.store(c.captures, s.capture, c.load(c.scratch, s.scratch))
			}
			b.Jump(fail)
		})
	case p.NdNonCapture, p.NdInlineOption:
		if e.NumChildren() > 0 {
			c.emit(e.Child(0), f, next, fail)
		} else {
			b.Bind(f.match)
			b.Jump(next)
			c.coldBlock(func() { b.Bind(f.back); b.Jump(fail) })
		}
	case p.NdRepeat:
		c.repeat(e, f, next, fail)
	case p.NdLookahead, p.NdNegLookahead, p.NdLookbehind, p.NdNegLookbehind:
		c.assertion(e, f, next, fail)
	case p.NdBackref:
		c.backreference(e, f, next, fail)
	case p.NdLiteral, p.NdCharClass, p.NdCharType, p.NdDot:
		b.Bind(f.match)
		c.checkCharacter(e, fail)
		b.Set(c.pos, b.Add(b.Get(c.pos), b.ConstNative(1)))
		b.Jump(next)
		c.coldBlock(func() { b.Bind(f.back); b.Set(c.pos, b.Sub(b.Get(c.pos), b.ConstNative(1))); b.Jump(fail) })
	default:
		b.Bind(f.match)
		c.tick()
		switch e.Kind() {
		case p.NdAnchorBeginText:
			b.Branch(b.CompareImmediate(b.Get(c.pos), ir.BranchNotEqual, 0), fail)
		case p.NdAnchorStartOfMatch:
			b.Branch(b.Compare(b.Get(c.pos), ir.BranchNotEqual, c.entryStart), fail)
		case p.NdAnchorEndText:
			b.Branch(b.Compare(b.Get(c.pos), ir.BranchNotEqual, c.end), fail)
		case p.NdAnchorBeginLine:
			c.beginLine(e.Flags(), fail)
		case p.NdAnchorEndLine:
			c.endLine(e.Flags(), false, fail)
		case p.NdAnchorEndTextOpt:
			c.endLine(e.Flags()&^p.Multiline, true, fail)
		case p.NdWordBoundary:
			c.wordBoundary(false, fail)
		case p.NdNonWordBoundary:
			c.wordBoundary(true, fail)
		}
		b.Jump(next)
		c.coldBlock(func() { b.Bind(f.back); b.Jump(fail) })
	}
}

func (c *emitter) repeat(e *p.Expression, f fragment, next, fail n.Label) {
	b := c.b
	min, max := e.Bounds()
	if max == 0 {
		b.Bind(f.match)
		b.Jump(next)
		c.coldBlock(func() { b.Bind(f.back); b.Jump(fail) })
		return
	}
	chars, captures, ok := fixedUnit(e.Child(0))
	if !ok {
		c.bounded(e, f, next, fail)
		return
	}
	width := len(chars)
	min *= width
	if max >= 0 {
		max *= width
	}
	type savedCapture struct{ capture, slot int }
	var saved []savedCapture
	for _, span := range captures {
		saved = append(saved, savedCapture{span.index * 2, c.slot()}, savedCapture{span.index*2 + 1, c.slot()})
	}
	begin := c.slot()
	loop, yield, undo, consume := b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Bind(f.match)
	c.store(c.scratch, begin, b.Get(c.pos))
	for _, s := range saved {
		c.store(c.scratch, s.slot, c.load(c.captures, s.capture))
	}
	b.Jump(loop)
	b.Bind(loop)
	count := b.Sub(b.Get(c.pos), c.load(c.scratch, begin))
	if e.Greediness() == p.Lazy {
		b.Branch(b.CompareImmediate(count, ir.BranchAboveEqual, int64(min)), yield)
	}
	if max >= 0 {
		b.Branch(b.CompareImmediate(count, ir.BranchAboveEqual, int64(max)), yield)
	}
	b.Jump(consume)
	b.Bind(consume)
	c.consumeUnit(chars, loop, yield)
	b.Bind(yield)
	b.Branch(b.CompareImmediate(b.Sub(b.Get(c.pos), c.load(c.scratch, begin)), ir.BranchBelow, int64(min)), undo)
	if len(captures) > 0 {
		empty, ready := b.NewLabel(), b.NewLabel()
		b.Branch(b.Compare(b.Get(c.pos), ir.BranchEqual, c.load(c.scratch, begin)), empty)
		for _, span := range captures {
			unitStart := b.Sub(b.Get(c.pos), b.ConstNative(uintptr(width)))
			c.store(c.captures, span.index*2, b.Add(unitStart, b.ConstNative(uintptr(span.start))))
			c.store(c.captures, span.index*2+1, b.Add(unitStart, b.ConstNative(uintptr(span.end))))
		}
		b.Jump(ready)
		b.Bind(empty)
		for _, s := range saved {
			c.store(c.captures, s.capture, c.load(c.scratch, s.slot))
		}
		b.Jump(ready)
		b.Bind(ready)
	}
	b.Jump(next)
	c.coldBlock(func() {
		b.Bind(f.back)
		c.tick()
		count := b.Sub(b.Get(c.pos), c.load(c.scratch, begin))
		switch e.Greediness() {
		case p.Lazy:
			if max >= 0 {
				b.Branch(b.CompareImmediate(count, ir.BranchAboveEqual, int64(max)), undo)
			}
			c.consumeUnit(chars, yield, undo)
		case p.Possessive:
			b.Jump(undo)
		default:
			b.Branch(b.CompareImmediate(count, ir.BranchBelowEqual, int64(min)), undo)
			b.Set(c.pos, b.Sub(b.Get(c.pos), b.ConstNative(uintptr(width))))
			b.Jump(yield)
		}
		b.Bind(undo)
		b.Set(c.pos, c.load(c.scratch, begin))
		for _, s := range saved {
			c.store(c.captures, s.capture, c.load(c.scratch, s.slot))
		}
		b.Jump(fail)
	})
}
