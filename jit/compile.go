package jit

import (
	"fmt"
	"runtime"

	p "github.com/KarpelesLab/gopcre2"
	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

type fragment struct{ match, back n.Label }
type emitter struct {
	cold                                      []func()
	openCaptures                              map[int][2]int
	b                                         *n.Builder
	input, end, captures, scratch, entryStart n.Value
	pos, start, work                          n.Var
	limit                                     n.Value
	limitExit                                 n.Label
	slots                                     int
	flags                                     p.Flag
}

func compileNative(pattern *p.Pattern) (*execution.Code, int, error) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return nil, 0, fmt.Errorf("%w: platform", ErrUnsupported)
	}
	allowed := p.Caseless | p.Multiline | p.DotAll | p.Extended | p.ExtendedMore | p.Anchored | p.EndAnchored |
		p.DollarEndOnly | p.Ungreedy | p.UTF | p.UCP | p.DupNames | p.NewlineLF | p.NewlineCR | p.NewlineCRLF |
		p.NewlineAny | p.NewlineAnyCRLF | p.NewlineNUL | p.BSRUnicode | p.BSRAnyCRLF |
		p.NoAutoPos | p.NoDotStarAnchor | p.NoStartOptimize
	if pattern.Flags()&^allowed != 0 {
		return nil, 0, fmt.Errorf("%w: compile options", ErrUnsupported)
	}
	budget := 512
	if err := supported(pattern.Root(), &budget, pattern.NumCapture()); err != nil {
		return nil, 0, err
	}
	b := n.NewBuilder()
	arg := n.PointerParam[state](b)
	c := &emitter{b: b, flags: pattern.Flags(), limitExit: b.NewLabel()}
	c.input = b.Load64(arg.At("Input").Addr())
	c.end = arg.At("Input").Len()
	c.captures = b.Load64(arg.At("Captures").Addr())
	c.scratch = b.Load64(arg.At("Scratch").Addr())
	c.limit = arg.Field("Limit")
	c.entryStart = arg.Field("Start")
	runtimeAnchored := arg.Field("Anchored")
	notEmpty := arg.Field("NotEmptyAtStart")
	c.pos = b.Var(c.entryStart)
	c.start = b.Var(c.entryStart)
	c.work = b.Var(b.ConstNative(0))
	anchored := pattern.Flags()&p.Anchored != 0 || pattern.Flags()&p.NoStartOptimize == 0 && pattern.AnchoredAtStart()
	search, success, retry, done := b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel()
	root := c.fragment()
	b.Jump(search)
	b.Bind(search)
	remaining := b.Sub(c.end, b.Get(c.start))
	b.Branch(b.CompareImmediate(remaining, ir.BranchBelow, int64(pattern.MinBytes())), done)
	if !anchored && pattern.Flags()&p.NoStartOptimize == 0 {
		c.checkStartSet(pattern.Root(), b.Get(c.start), retry)
	}
	if r, flags, offset, found, _ := firstLiteral(pattern.Root()); found && offset > 0 && offset <= 8 && !(flags&p.Caseless != 0 && r >= 128) && !anchored && pattern.Flags()&p.NoStartOptimize == 0 {
		at := b.Add(b.Get(c.start), b.ConstNative(uintptr(offset)))
		b.Branch(b.Compare(at, ir.BranchAboveEqual, c.end), done)
		c.tick()
		ch := c.charAt(at)
		if flags&p.Caseless != 0 && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			ch = b.Or(ch, b.ConstNative(32))
			r |= 32
		}
		b.Branch(b.CompareImmediate(ch, ir.BranchNotEqual, int64(r)), retry)
	}
	b.Set(c.pos, b.Get(c.start))
	b.Jump(root.match)
	c.emit(pattern.Root(), root, success, retry)
	b.Bind(success)
	emptyAtEntry := b.CompareImmediate(notEmpty, ir.BranchNotEqual, 0).And(b.Compare(b.Get(c.pos), ir.BranchEqual, b.Get(c.start))).And(b.Compare(b.Get(c.start), ir.BranchEqual, c.entryStart))
	b.Branch(emptyAtEntry, root.back)
	if pattern.Flags()&p.EndAnchored != 0 {
		b.Branch(b.Compare(b.Get(c.pos), ir.BranchNotEqual, c.end), root.back)
	}
	c.store(c.captures, 0, b.Get(c.start))
	c.store(c.captures, 1, b.Get(c.pos))
	b.Return(b.ConstNative(1))
	// Emit restoration paths in reverse order, after successful matching.
	// The backend can make adjacent matching/undo edges fall through.
	for len(c.cold) > 0 {
		i := len(c.cold) - 1
		emit := c.cold[i]
		c.cold = c.cold[:i]
		emit()
	}
	b.Bind(retry)
	if anchored {
		b.Jump(done)
	} else {
		b.Branch(b.CompareImmediate(runtimeAnchored, ir.BranchNotEqual, 0), done)
		b.Branch(b.Compare(b.Get(c.start), ir.BranchAboveEqual, c.end), done)
		b.Set(c.start, b.Add(b.Get(c.start), b.ConstNative(1)))
		// Do not restart between the two bytes of a CRLF newline.
		if c.crlf() {
			again := b.NewLabel()
			b.Branch(b.Compare(b.Get(c.start), ir.BranchAboveEqual, c.end), search)
			b.Branch(b.CompareImmediate(c.charAt(b.Get(c.start)), ir.BranchNotEqual, '\n'), search)
			prev := c.charAt(b.Sub(b.Get(c.start), b.ConstNative(1)))
			b.Branch(b.CompareImmediate(prev, ir.BranchNotEqual, '\r'), search, again)
			b.Bind(again)
			b.Set(c.start, b.Add(b.Get(c.start), b.ConstNative(1)))
		}
		b.Jump(search)
	}
	b.Bind(done)
	b.Return(b.ConstNative(0))
	b.Bind(c.limitExit)
	b.Return(b.ConstNative(2))
	program, err := b.Finish()
	if err != nil {
		return nil, 0, err
	}
	code, err := n.Compile(program, compile.TargetSpec{}, compile.Func{
		ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640},
	})
	return code, c.slots, err
}

func supported(e *p.Expression, budget *int, captures int) error {
	*budget--
	if *budget < 0 {
		return fmt.Errorf("%w: pattern size", ErrUnsupported)
	}
	switch e.Kind() {
	case p.NdLiteral:
		if e.Rune() < 0 || e.Rune() > 255 || (e.Flags()&p.UTF != 0 && e.Rune() > 127) {
			return fmt.Errorf("%w: literal outside native encoding subset", ErrUnsupported)
		}
	case p.NdCharClass:
		for i := 0; i < e.NumRanges(); i++ {
			r := e.Range(i)
			if r.Lo < 0 || r.Hi > 0x10ffff {
				return fmt.Errorf("%w: class outside native encoding subset", ErrUnsupported)
			}
		}
		for i := 0; i < e.NumChildren(); i++ {
			if e.Child(i).Kind() != p.NdCharType {
				return fmt.Errorf("%w: class property", ErrUnsupported)
			}
		}
	case p.NdCharType:
		if e.Name() != "" {
			return fmt.Errorf("%w: special character escape", ErrUnsupported)
		}
	case p.NdRepeat:
		min, max := e.Bounds()
		if min < 0 || (max >= 0 && max < min) {
			return fmt.Errorf("%w: repeat bounds", ErrUnsupported)
		}
		if min == 0 && max == 0 {
			return nil
		}
		if chars, _, ok := fixedUnit(e.Child(0)); ok {
			*budget -= len(chars)
			if *budget < 0 {
				return fmt.Errorf("%w: expanded fixed unit", ErrUnsupported)
			}
		} else {
			if max < 0 || max > 8 || e.Greediness() == p.Possessive {
				return fmt.Errorf("%w: repeated group", ErrUnsupported)
			}
			for i := 0; i < max; i++ {
				if err := supported(e.Child(0), budget, captures); err != nil {
					return err
				}
			}
			return nil
		}
	case p.NdBackref:
		if e.Name() != "" || e.Capture() <= 0 || e.Capture() > captures {
			return fmt.Errorf("%w: backreference target", ErrUnsupported)
		}
	case p.NdLookbehind, p.NdNegLookbehind:
		for _, branch := range lookbehindBranches(e.Child(0)) {
			if _, ok := fixedLookbehindWidth(branch); !ok {
				return fmt.Errorf("%w: variable lookbehind branch", ErrUnsupported)
			}
		}
	case p.NdEmpty, p.NdConcat, p.NdAlternate, p.NdCapture, p.NdNamedCapture, p.NdNonCapture,
		p.NdAtomicGroup, p.NdDot, p.NdAnchorBeginText, p.NdAnchorEndText, p.NdAnchorBeginLine,
		p.NdAnchorEndLine, p.NdAnchorEndTextOpt, p.NdAnchorStartOfMatch, p.NdInlineOption,
		p.NdLookahead, p.NdNegLookahead, p.NdWordBoundary, p.NdNonWordBoundary:
	default:
		return fmt.Errorf("%w: expression kind %d", ErrUnsupported, e.Kind())
	}
	for i := 0; i < e.NumChildren(); i++ {
		if err := supported(e.Child(i), budget, captures); err != nil {
			return err
		}
	}
	return nil
}

func (c *emitter) fragment() fragment    { return fragment{c.b.NewLabel(), c.b.NewLabel()} }
func (c *emitter) coldBlock(emit func()) { c.cold = append(c.cold, emit) }
func (c *emitter) slot() int             { s := c.slots; c.slots++; return s }
func (c *emitter) address(base n.Value, slot int) n.Value {
	return c.b.Add(base, c.b.ConstNative(uintptr(slot*8)))
}
func (c *emitter) load(base n.Value, slot int) n.Value { return c.b.Load64(c.address(base, slot)) }
func (c *emitter) store(base n.Value, slot int, value n.Value) {
	c.b.Store64(c.address(base, slot), value)
}
func (c *emitter) charAt(pos n.Value) n.Value {
	return c.b.CastNative(c.b.Load8(c.b.Add(c.input, pos)))
}
func (c *emitter) tick() {
	b := c.b
	b.Set(c.work, b.Add(b.Get(c.work), b.ConstNative(1)))
	b.Branch(b.Compare(b.Get(c.work), ir.BranchAbove, c.limit), c.limitExit)
}
func (c *emitter) crlf() bool { return c.flags&(p.NewlineCRLF|p.NewlineAnyCRLF|p.NewlineAny) != 0 }
