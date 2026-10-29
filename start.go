package gopcre2

import (
	"unicode/utf8"
)

// A conservative FIRST set. nullable suppresses candidate rejection; zero
// proves that a preceding node consumes no subject, for implicit anchoring.
type startInfo struct {
	bytes                           [4]uint64
	known, nullable, zero, anchored bool
}

func (e *Expression) StartBytes() ([4]uint64, bool) {
	return e.start.bytes, e.start.known && !e.start.nullable && !fullBytes(e.start.bytes)
}
func (p *Pattern) AnchoredAtStart() bool { return p.flags&Anchored != 0 || p.root.start.anchored }
func fullBytes(set [4]uint64) bool {
	return set == [4]uint64{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)}
}
func byteIn(set [4]uint64, b byte) bool { return set[b>>6]&(uint64(1)<<(b&63)) != 0 }
func addByte(set *[4]uint64, b byte)    { set[b>>6] |= uint64(1) << (b & 63) }
func unionBytes(a *[4]uint64, b [4]uint64) {
	for i := range a {
		a[i] |= b[i]
	}
}

func analyzeStart(e *Expression) startInfo {
	empty := startInfo{known: true, nullable: true, zero: true}
	unknown := startInfo{nullable: true}
	switch e.Kind() {
	case NdEmpty, NdCallout, NdWordBoundary, NdNonWordBoundary, NdLookahead, NdNegLookahead, NdLookbehind, NdNegLookbehind, NdAnchorEndText, NdAnchorEndTextOpt, NdAnchorEndLine, NdMatchPointReset:
		return empty
	case NdAnchorBeginText, NdAnchorStartOfMatch:
		empty.anchored = true
		return empty
	case NdAnchorBeginLine:
		empty.anchored = e.Flags()&Multiline == 0
		return empty
	case NdCapture, NdNamedCapture, NdNonCapture, NdAtomicGroup, NdBranchReset, NdInlineOption:
		if e.NumChildren() == 0 {
			return empty
		}
		return e.Child(0).start
	case NdRepeat:
		min, max := e.Bounds()
		if max == 0 {
			return empty
		}
		r := e.Child(0).start
		r.nullable = min == 0 || r.nullable
		r.anchored = min > 0 && r.anchored
		return r
	case NdConcat:
		r := empty
		open, anchorOpen := true, true
		for _, child := range e.children {
			s := child.start
			if open {
				unionBytes(&r.bytes, s.bytes)
				r.known = r.known && s.known
				open = s.nullable
			}
			if anchorOpen && s.anchored {
				r.anchored = true
			}
			anchorOpen = anchorOpen && s.zero
			r.zero = r.zero && s.zero
			r.nullable = r.nullable && s.nullable
		}
		return r
	case NdAlternate:
		r := startInfo{known: true, zero: true, anchored: e.NumChildren() > 0}
		for _, child := range e.children {
			s := child.start
			unionBytes(&r.bytes, s.bytes)
			r.known = r.known && s.known
			r.nullable = r.nullable || s.nullable
			r.zero = r.zero && s.zero
			r.anchored = r.anchored && s.anchored
		}
		return r
	case NdLiteral:
		r := startInfo{known: true}
		ch := e.Rune()
		for folded := ch; ; folded = foldCharacter(folded, e.Flags()) {
			if e.Flags()&UTF != 0 {
				var b [4]byte
				n := utf8.EncodeRune(b[:], folded)
				if n > 0 {
					addByte(&r.bytes, b[0])
				}
			} else if folded >= 0 && folded < 256 {
				addByte(&r.bytes, byte(folded))
			}
			if e.Flags()&Caseless == 0 || foldCharacter(folded, e.Flags()) == ch {
				break
			}
		}
		return r
	case NdCharClass, NdCharType, NdProperty:
		r := startInfo{known: true}
		v := vm{flags: e.Flags()}
		for i := 0; i < 256; i++ {
			if e.Flags()&UTF != 0 && i >= 128 {
				break
			}
			if startCharacter(e, rune(i), &v) {
				addByte(&r.bytes, byte(i))
			}
		}
		if e.Flags()&UTF != 0 {
			for i := 0xc2; i <= 0xf4; i++ {
				addByte(&r.bytes, byte(i))
			}
		}
		return r
	case NdDot:
		return startInfo{}
	}
	return unknown
}

func startCharacter(e *Expression, ch rune, v *vm) bool {
	switch e.Kind() {
	case NdCharType:
		return v.matchCharType(ch, e.CharType())
	case NdProperty:
		matched := matchProperty(ch, e.Name())
		return matched != e.Negated()
	case NdCharClass:
		matched := false
		for folded := ch; ; folded = foldCharacter(folded, e.Flags()) {
			for _, rr := range e.node.Ranges {
				if folded >= rr.Lo && folded <= rr.Hi {
					matched = true
				}
			}
			if e.Flags()&Caseless == 0 || foldCharacter(folded, e.Flags()) == ch {
				break
			}
		}
		for _, child := range e.children {
			if startCharacter(child, ch, v) {
				matched = true
			}
		}
		return matched != e.Negated()
	}
	return false
}
