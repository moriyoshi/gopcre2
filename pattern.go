package gopcre2

// Pattern is the immutable, structured representation shared by pattern
// compilers. Repetition and group structure are retained before VM lowering.
type Pattern struct {
	root     *Expression
	flags    Flag
	names    []string
	minBytes int
}

func (e *Expression) Name() string { return e.node.Name }

// Expression is a read-only view of a parsed expression. Flags reports the
// effective options at this expression, including preceding inline options.
type Expression struct {
	node     *Node
	children []*Expression
	flags    Flag
	minBytes int
}

func (p *Pattern) Root() *Expression          { return p.root }
func (p *Pattern) Flags() Flag                { return p.flags }
func (p *Pattern) MinBytes() int              { return p.minBytes }
func (p *Pattern) NumCapture() int            { return len(p.names) - 1 }
func (e *Expression) Kind() NodeKind          { return e.node.Kind }
func (e *Expression) Flags() Flag             { return e.flags }
func (e *Expression) NumChildren() int        { return len(e.children) }
func (e *Expression) Child(i int) *Expression { return e.children[i] }
func (e *Expression) Rune() rune              { return e.node.Rune }
func (e *Expression) NumRanges() int          { return len(e.node.Ranges) }
func (e *Expression) Range(i int) RuneRange   { return e.node.Ranges[i] }
func (e *Expression) Negated() bool           { return e.node.Negate }
func (e *Expression) Capture() int            { return e.node.Index }
func (e *Expression) Bounds() (int, int)      { return e.node.Min, e.node.Max }
func (e *Expression) Greediness() GreedyKind  { return e.node.Greedy }
func (e *Expression) CharType() CharTypeKind  { return e.node.CharType }
func (e *Expression) MinBytes() int           { return e.minBytes }

// Pattern returns the read-only structured pattern. Its lifetime is independent
// of mutable match settings and it is safe to share between compilers.
func (re *Regexp) Pattern() *Pattern { return re.prog.pattern }

func analyzePattern(root *Node, flags Flag) *Pattern {
	p := &Pattern{flags: flags, names: []string{""}}
	safeMin := true
	var visit func(*Node, Flag) (*Expression, Flag)
	visit = func(n *Node, f Flag) (*Expression, Flag) {
		copyNode := *n
		copyNode.Ranges = append([]RuneRange(nil), n.Ranges...)
		copyNode.Children = nil
		e := &Expression{node: &copyNode, flags: f}
		if n.Kind == NdCapture || n.Kind == NdNamedCapture {
			for len(p.names) <= n.Index {
				p.names = append(p.names, "")
			}
			if n.Kind == NdNamedCapture {
				p.names[n.Index] = n.Name
			}
		}
		innerFlags := f
		if n.Kind == NdInlineOption {
			innerFlags = (f | n.Flags) &^ n.UnFlags
		}
		for _, ch := range n.Children {
			child, next := visit(ch, innerFlags)
			e.children = append(e.children, child)
			copyNode.Children = append(copyNode.Children, child.node)
			// Options propagate across concatenation and alternatives in the same
			// group, but a group's options do not escape its enclosing boundary.
			if n.Kind == NdConcat || n.Kind == NdAlternate {
				innerFlags = next
			}
		}
		switch n.Kind {
		case NdLiteral:
			e.minBytes = 1 // conservative for byte mode and Unicode case folding
		case NdDot, NdCharClass, NdCharType, NdProperty:
			e.minBytes = 1
		case NdConcat:
			for _, c := range e.children {
				e.minBytes = safeLengthAdd(e.minBytes, c.minBytes)
			}
		case NdAlternate:
			if len(e.children) > 0 {
				e.minBytes = e.children[0].minBytes
				for _, c := range e.children[1:] {
					if c.minBytes < e.minBytes {
						e.minBytes = c.minBytes
					}
				}
			}
		case NdRepeat:
			// Saturating arithmetic never overestimates a required length.
			if len(e.children) > 0 && n.Min > 0 {
				m := e.children[0].minBytes
				if m > 0 && n.Min <= int(^uint(0)>>1)/m {
					e.minBytes = m * n.Min
				}
			}
		case NdCapture, NdNamedCapture, NdNonCapture, NdAtomicGroup, NdBranchReset:
			if len(e.children) > 0 {
				c := e.children[0]
				e.minBytes = c.minBytes
			}
		case NdInlineOption:
			if len(e.children) > 0 {
				c := e.children[0]
				e.minBytes = c.minBytes
			}
		case NdVerb, NdRecursion, NdSubroutineCall:
			// ACCEPT, including inside a subroutine, can terminate the entire
			// match before a later consuming suffix is reached.
			safeMin = false
		}
		next := f
		if n.Kind == NdInlineOption && len(n.Children) == 0 {
			next = innerFlags
		}
		if n.Kind == NdConcat || n.Kind == NdAlternate {
			next = innerFlags
		}
		return e, next
	}
	p.root, _ = visit(root, flags)
	if safeMin {
		p.minBytes = p.root.minBytes
	}
	return p
}

func safeLengthAdd(a, b int) int {
	if a > int(^uint(0)>>1)-b {
		return 0
	}
	return a + b
}
