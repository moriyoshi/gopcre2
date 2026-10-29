package jit

import (
	"unicode"

	p "github.com/KarpelesLab/gopcre2"
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

func (c *emitter) checkCharacter(e *p.Expression, fail n.Label) {
	b := c.b
	c.tick()
	pos := b.Get(c.pos)
	b.Branch(b.Compare(pos, ir.BranchAboveEqual, c.end), fail)
	ch := c.charAt(pos)
	if e.Kind() == p.NdDot && e.Flags()&p.DotAll == 0 && c.flags&p.NewlineCRLF != 0 {
		checkLF, done := b.NewLabel(), b.NewLabel()
		b.Branch(b.CompareImmediate(ch, ir.BranchNotEqual, '\r'), checkLF)
		b.Branch(b.Compare(b.Add(pos, b.ConstNative(1)), ir.BranchAboveEqual, c.end), done)
		b.Branch(b.CompareImmediate(c.charAt(b.Add(pos, b.ConstNative(1))), ir.BranchEqual, '\n'), fail, done)
		b.Bind(checkLF)
		b.Branch(b.CompareImmediate(ch, ir.BranchNotEqual, '\n'), done)
		b.Branch(b.CompareImmediate(pos, ir.BranchEqual, 0), done)
		b.Branch(b.CompareImmediate(c.charAt(b.Sub(pos, b.ConstNative(1))), ir.BranchEqual, '\r'), fail, done)
		b.Bind(done)
	}
	var set [256]bool
	for i := range set {
		set[i] = characterMember(e, rune(i))
	}
	if e.Flags()&p.Caseless != 0 && (e.Kind() == p.NdLiteral || e.Kind() == p.NdCharClass) {
		original := set
		for i := range set {
			r := rune(i)
			if e.Flags()&(p.UTF|p.UCP) != 0 {
				for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
					if folded < 256 && original[folded] {
						set[i] = true
					}
				}
			} else if r >= 'A' && r <= 'Z' {
				set[i] = original[i] || original[i+32]
			} else if r >= 'a' && r <= 'z' {
				set[i] = original[i] || original[i-32]
			}
		}
	}
	if e.Kind() == p.NdCharClass && e.Negated() {
		for i := range set {
			set[i] = !set[i]
		}
	}
	// CRLF needs its neighboring-byte test above; its individual bytes pass.
	if e.Kind() == p.NdDot && e.Flags()&p.DotAll == 0 && c.flags&p.NewlineCRLF != 0 {
		set['\r'], set['\n'] = true, true
	}

	pred := b.CompareImmediate(ch, ir.BranchBelow, 0)
	for i := 0; i < len(set); i++ {
		if !set[i] {
			continue
		}
		lo := i
		for i+1 < len(set) && set[i+1] {
			i++
		}
		part := b.CompareImmediate(ch, ir.BranchEqual, int64(lo))
		if i > lo {
			part = b.CompareImmediate(ch, ir.BranchAboveEqual, int64(lo)).And(b.CompareImmediate(ch, ir.BranchBelowEqual, int64(i)))
		}
		pred = pred.Or(part)
	}
	b.Branch(pred.Not(), fail)
}

func (c *emitter) beginLine(flags p.Flag, fail n.Label) {
	b := c.b
	pass := b.NewLabel()
	pos := b.Get(c.pos)
	b.Branch(b.CompareImmediate(pos, ir.BranchEqual, 0), pass)
	if flags&p.Multiline == 0 {
		b.Jump(fail)
	} else {
		// ^ does not match the empty line after a final newline by default.
		b.Branch(b.Compare(pos, ir.BranchAboveEqual, c.end), fail)
		prev := c.charAt(b.Sub(pos, b.ConstNative(1)))
		switch {
		case c.flags&p.NewlineNUL != 0:
			b.Branch(b.CompareImmediate(prev, ir.BranchEqual, 0), pass, fail)
		case c.flags&p.NewlineCR != 0:
			b.Branch(b.CompareImmediate(prev, ir.BranchEqual, '\r'), pass, fail)
		case c.flags&p.NewlineCRLF != 0:
			b.Branch(b.CompareImmediate(prev, ir.BranchNotEqual, '\n'), fail)
			b.Branch(b.CompareImmediate(pos, ir.BranchBelow, 2), fail)
			b.Branch(b.CompareImmediate(c.charAt(b.Sub(pos, b.ConstNative(2))), ir.BranchEqual, '\r'), pass, fail)
		case c.crlf():
			b.Branch(b.CompareImmediate(prev, ir.BranchEqual, '\n'), pass)
			if c.flags&p.NewlineAny != 0 {
				b.Branch(b.CompareImmediate(prev, ir.BranchAboveEqual, '\v').And(b.CompareImmediate(prev, ir.BranchBelowEqual, '\f')).Or(b.CompareImmediate(prev, ir.BranchEqual, 0x85)), pass)
			}
			b.Branch(b.CompareImmediate(prev, ir.BranchNotEqual, '\r'), fail)
			b.Branch(b.CompareImmediate(c.charAt(pos), ir.BranchNotEqual, '\n'), pass, fail)
		default:
			b.Branch(b.CompareImmediate(prev, ir.BranchEqual, '\n'), pass, fail)
		}
	}
	b.Bind(pass)
}

func (c *emitter) endLine(flags p.Flag, forceOptional bool, fail n.Label) {
	b := c.b
	pass := b.NewLabel()
	pos := b.Get(c.pos)
	b.Branch(b.Compare(pos, ir.BranchEqual, c.end), pass)
	if !forceOptional && flags&p.Multiline == 0 && flags&p.DollarEndOnly != 0 {
		b.Jump(fail)
		b.Bind(pass)
		return
	}
	one, pair := b.NewLabel(), b.NewLabel()
	ch := c.charAt(pos) // pos <= end is an invariant; end was excluded above.
	switch {
	case c.flags&p.NewlineNUL != 0:
		b.Branch(b.CompareImmediate(ch, ir.BranchEqual, 0), one, fail)
	case c.flags&p.NewlineCR != 0:
		b.Branch(b.CompareImmediate(ch, ir.BranchEqual, '\r'), one, fail)
	case c.crlf():
		lf := b.NewLabel()
		b.Branch(b.CompareImmediate(ch, ir.BranchNotEqual, '\r'), lf)
		if c.flags&p.NewlineCRLF == 0 {
			b.Branch(b.Compare(b.Add(pos, b.ConstNative(1)), ir.BranchEqual, c.end), one)
		} else {
			b.Branch(b.Compare(b.Add(pos, b.ConstNative(1)), ir.BranchAboveEqual, c.end), fail)
		}
		b.Branch(b.CompareImmediate(c.charAt(b.Add(pos, b.ConstNative(1))), ir.BranchEqual, '\n'), pair)
		if c.flags&p.NewlineCRLF != 0 {
			b.Jump(fail)
		} else {
			b.Jump(one)
		}
		b.Bind(lf)
		if c.flags&p.NewlineAny != 0 {
			b.Branch(b.CompareImmediate(ch, ir.BranchAboveEqual, '\v').And(b.CompareImmediate(ch, ir.BranchBelowEqual, '\f')).Or(b.CompareImmediate(ch, ir.BranchEqual, 0x85)), one)
		}
		b.Branch(b.CompareImmediate(ch, ir.BranchNotEqual, '\n'), fail)
		if c.flags&p.NewlineCRLF != 0 {
			b.Jump(fail)
		} else {
			b.Branch(b.CompareImmediate(pos, ir.BranchEqual, 0), one)
			b.Branch(b.CompareImmediate(c.charAt(b.Sub(pos, b.ConstNative(1))), ir.BranchNotEqual, '\r'), one, fail)
		}
	default:
		b.Branch(b.CompareImmediate(ch, ir.BranchEqual, '\n'), one, fail)
	}
	b.Bind(pair)
	if flags&p.Multiline == 0 {
		b.Branch(b.Compare(b.Add(pos, b.ConstNative(2)), ir.BranchNotEqual, c.end), fail)
	}
	b.Jump(pass)
	b.Bind(one)
	if flags&p.Multiline == 0 {
		b.Branch(b.Compare(b.Add(pos, b.ConstNative(1)), ir.BranchNotEqual, c.end), fail)
	}
	b.Jump(pass)
	b.Bind(pass)
}

// All membership decisions are made at compile time; generated byte matchers
// contain only ranges and branches, including UCP's Latin-1 membership.
func characterMember(e *p.Expression, r rune) bool {
	switch e.Kind() {
	case p.NdLiteral:
		return r == e.Rune()
	case p.NdCharClass:
		for i := 0; i < e.NumRanges(); i++ {
			rr := e.Range(i)
			if r >= rr.Lo && r <= rr.Hi {
				return true
			}
		}
		for i := 0; i < e.NumChildren(); i++ {
			if characterMember(e.Child(i), r) {
				return true
			}
		}
		return false
	case p.NdDot:
		if e.Flags()&p.DotAll != 0 {
			return true
		}
		switch {
		case e.Flags()&p.NewlineNUL != 0:
			return r != 0
		case e.Flags()&p.NewlineCR != 0:
			return r != '\r'
		case e.Flags()&p.NewlineCRLF != 0:
			return true
		case e.Flags()&p.NewlineAny != 0:
			return !(r >= '\n' && r <= '\r') && r != 0x85
		case e.Flags()&p.NewlineAnyCRLF != 0:
			return r != '\r' && r != '\n'
		default:
			return r != '\n'
		}
	case p.NdCharType:
		var match bool
		switch e.CharType() {
		case p.CharTypeDigit, p.CharTypeNonDigit:
			if e.Flags()&p.UCP != 0 {
				match = unicode.IsDigit(r)
			} else {
				match = r >= '0' && r <= '9'
			}
		case p.CharTypeWord, p.CharTypeNonWord:
			match = wordMember(r, e.Flags())
		case p.CharTypeSpace, p.CharTypeNonSpace:
			if e.Flags()&p.UCP != 0 {
				match = unicode.IsSpace(r)
			} else {
				match = r == ' ' || r >= '\t' && r <= '\r'
			}
		case p.CharTypeHSpace, p.CharTypeNonHSpace:
			match = r == ' ' || r == '\t' || r == 0xa0
		case p.CharTypeVSpace, p.CharTypeNonVSpace:
			match = r >= '\n' && r <= '\r' || r == 0x85
		}
		if e.CharType()%2 == 1 {
			return !match
		}
		return match
	}
	return false
}

func wordMember(r rune, flags p.Flag) bool {
	if flags&p.UCP != 0 {
		return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Pc, r)
	}
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
}
