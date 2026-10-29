package gopcre2

import (
	"unicode"
)

// vm executes a compiled Program against a subject string.
type vm struct {
	child           *vm
	notEmptyAtStart bool
	searchStart     int
	loopPositions   []int
	prog            *Program
	subject         string
	pos             int   // current position in subject
	matchStart      int   // where the reported match starts (\K can change this)
	startPos        int   // where the current match attempt began (\G)
	captures        []int // ovector: pairs of [start, end) per group; -1 = unset
	stack           *btStack
	flags           Flag   // runtime flags
	steps           int    // step counter for match limit
	depth           int    // current recursion depth
	mark            string // most recent (*MARK:NAME) value
	backtrackPC     uint32 // resume point belongs to this invocation

	// Limits
	matchLimit int
	depthLimit int
	heapLimit  int

	// Callout
	callout CalloutFunc

	// Error from limit exceeded
	limitErr error
}

func newVM(prog *Program, subject string, startPos int) *vm {
	numSlots := (prog.NumCapture + 1) * 2
	caps := make([]int, numSlots)
	for i := range caps {
		caps[i] = -1
	}

	matchLimit := prog.MatchLimit
	if matchLimit == 0 {
		matchLimit = DefaultMatchLimit
	}
	depthLimit := prog.DepthLimit
	if depthLimit == 0 {
		depthLimit = DefaultDepthLimit
	}
	heapLimit := prog.HeapLimit
	if heapLimit == 0 {
		heapLimit = DefaultHeapLimit
	}

	return &vm{
		prog:       prog,
		subject:    subject,
		pos:        startPos,
		matchStart: startPos,
		startPos:   startPos,
		captures:   caps,
		stack:      newBTStack(),
		flags:      prog.Flags,
		matchLimit: matchLimit,
		depthLimit: depthLimit,
		heapLimit:  heapLimit,
	}
}

// exec runs the VM from the current position. Returns true if a match was found.
func (v *vm) exec() bool {
	return v.execFrom(uint32(v.prog.Start), -1, -1)
}

// A child uses the same dispatcher, bounded by its enclosing assertion/group.
func (v *vm) execFrom(pc uint32, endPC, targetPos int) bool {
	for {
		// Check limits
		v.steps++
		if v.steps > v.matchLimit {
			v.limitErr = ErrMatchLimit
			return false
		}
		if v.stack.heapUsed > v.heapLimit {
			v.limitErr = ErrHeapLimit
			return false
		}

		if int(pc) == endPC {
			if targetPos < 0 || v.pos == targetPos {
				return true
			}
			if !v.backtrack() {
				return false
			}
			pc = v.nextPC()
			continue
		}
		if int(pc) >= len(v.prog.Inst) {
			return false
		}
		inst := &v.prog.Inst[pc]

		switch inst.Op {
		case OpMatch:
			if endPC < 0 && v.notEmptyAtStart && v.pos == v.matchStart && v.matchStart == v.searchStart {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			// Set capture 0
			v.captures[0] = v.matchStart
			v.captures[1] = v.pos
			return true

		case OpFail:
			if !v.backtrack() {
				return false
			}
			pc = v.nextPC()
			continue

		case OpNop:
			pc = inst.Out
			continue
		case OpLoopBegin:
			v.saveProgress(inst.N)
			pc = inst.Out
		case OpLoopEnd:
			if v.loopProgressed(inst.N) {
				pc = inst.Out
			} else {
				pc = inst.Arg
			}

		case OpRune:
			if v.pos >= len(v.subject) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			r, size := decodeCharacter(v.subject[v.pos:], v.flags)
			if r != inst.Runes[0] {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			v.pos += size
			pc = inst.Out

		case OpRuneFold:
			if v.pos >= len(v.subject) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			r, size := decodeCharacter(v.subject[v.pos:], v.flags)
			if !equalFold(r, inst.Runes[0], v.flags) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			v.pos += size
			pc = inst.Out

		case OpAnyCharNotNL:
			if v.pos >= len(v.subject) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			r, size := decodeCharacter(v.subject[v.pos:], v.flags)
			if r == '\n' {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			_ = r
			v.pos += size
			pc = inst.Out

		case OpAnyChar:
			if v.pos >= len(v.subject) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			_, size := decodeCharacter(v.subject[v.pos:], v.flags)
			v.pos += size
			pc = inst.Out

		case OpCharClass:
			if v.pos >= len(v.subject) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			r, size := decodeCharacter(v.subject[v.pos:], v.flags)
			if !v.matchCharClass(r, inst) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			v.pos += size
			pc = inst.Out

		case OpCharType:
			if v.pos >= len(v.subject) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			r, size := decodeCharacter(v.subject[v.pos:], v.flags)
			if !v.matchCharType(r, CharTypeKind(inst.N)) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			v.pos += size
			pc = inst.Out

		case OpProperty, OpPropertyNeg:
			if v.pos >= len(v.subject) {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			r, size := decodeCharacter(v.subject[v.pos:], v.flags)
			match := matchProperty(r, inst.Str)
			if inst.Op == OpPropertyNeg {
				match = !match
			}
			if !match {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			v.pos += size
			pc = inst.Out

		case OpSplit:

			// Greedy: try Out first, save Arg as alternative
			v.stack.push(stackFrame{
				kind: frameSplit,
				pc:   inst.Arg,
				pos:  v.pos,
			})
			v.saveCapturesForSplit()
			pc = inst.Out

		case OpSplitLazy:
			// Lazy: try Out first (skip/done), save Arg as alternative (body)
			v.stack.push(stackFrame{
				kind: frameSplit,
				pc:   inst.Arg,
				pos:  v.pos,
			})
			v.saveCapturesForSplit()
			pc = inst.Out

		case OpJump:
			pc = inst.Out

		case OpCaptureStart:
			slot := inst.N * 2
			if slot < len(v.captures) {
				v.stack.push(stackFrame{
					kind:    frameCapture,
					capSlot: slot,
					capVal:  v.captures[slot],
				})
				v.captures[slot] = v.pos
			}
			pc = inst.Out

		case OpCaptureEnd:
			slot := inst.N*2 + 1
			if slot < len(v.captures) {
				v.stack.push(stackFrame{
					kind:    frameCapture,
					capSlot: slot,
					capVal:  v.captures[slot],
				})
				v.captures[slot] = v.pos
			}
			pc = inst.Out

		case OpAssertBeginLine:
			if v.pos == 0 || (v.flags&Multiline != 0 && v.pos > 0 && v.pos < len(v.subject) && v.subject[v.pos-1] == '\n') {
				pc = inst.Out
			} else {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
			}

		case OpAssertEndLine:
			atEnd := v.pos == len(v.subject)
			beforeNL := v.pos < len(v.subject) && v.subject[v.pos] == '\n'
			matched := false
			if v.flags&Multiline != 0 {
				matched = atEnd || beforeNL
			} else if v.flags&DollarEndOnly != 0 {
				// DollarEndOnly: $ only matches at the very end
				matched = atEnd
			} else {
				// Default: $ matches at end, or before final \n at end
				matched = atEnd || (beforeNL && v.pos+1 == len(v.subject))
			}
			if matched {
				pc = inst.Out
			} else {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
			}

		case OpAssertBeginText:
			if v.pos == 0 {
				pc = inst.Out
			} else {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
			}

		case OpAssertEndText:
			if v.pos == len(v.subject) {
				pc = inst.Out
			} else {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
			}

		case OpAssertEndTextOrNewline:
			atEnd := v.pos == len(v.subject)
			beforeFinalNL := v.pos+1 == len(v.subject) && v.subject[v.pos] == '\n'
			if atEnd || beforeFinalNL {
				pc = inst.Out
			} else {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
			}

		case OpAssertWordBoundary:
			if v.atWordBoundary() {
				pc = inst.Out
			} else {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
			}

		case OpAssertNonWordBoundary:
			if !v.atWordBoundary() {
				pc = inst.Out
			} else {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
			}

		case OpAssertStartOfMatch:
			if v.pos == v.startPos {
				pc = inst.Out
			} else {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
			}

		case OpResetMatchStart:
			v.stack.push(stackFrame{
				kind:       frameMark,
				matchStart: v.matchStart,
			})
			v.matchStart = v.pos
			pc = inst.Out

		case OpBackref, OpBackrefFold:
			group := inst.N
			if inst.Str != "" {
				for _, idx := range v.prog.NameToGroup[inst.Str] {
					if v.captures[idx*2] >= 0 && v.captures[idx*2+1] >= 0 {
						group = idx
						break
					}
				}
			}
			if group < 0 || group*2+1 >= len(v.captures) {
				// Backreference to non-existent group
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			groupStart := v.captures[group*2]
			groupEnd := v.captures[group*2+1]
			if groupStart < 0 || groupEnd < 0 {
				// Unset backreference
				if v.flags&MatchUnsetBackref != 0 {
					// Match empty string
					pc = inst.Out
				} else {
					if !v.backtrack() {
						return false
					}
					pc = v.nextPC()
				}
				continue
			}
			ref := v.subject[groupStart:groupEnd]
			if inst.Op == OpBackrefFold {
				if !v.matchStringFold(ref) {
					if !v.backtrack() {
						return false
					}
					pc = v.nextPC()
					continue
				}
			} else {
				remaining := v.subject[v.pos:]
				if len(remaining) < len(ref) || remaining[:len(ref)] != ref {
					if !v.backtrack() {
						return false
					}
					pc = v.nextPC()
					continue
				}
				v.pos += len(ref)
			}
			pc = inst.Out

		case OpLookaheadStart, OpNegLookaheadStart:
			negative := inst.Op == OpNegLookaheadStart
			endOp := OpLookaheadEnd
			if negative {
				endOp = OpNegLookaheadEnd
			}
			end := v.findLookaheadEnd(pc+1, endOp)
			sub, matched := v.runChild(pc+1, end, v.pos, -1)
			if v.limitErr != nil {
				return false
			}
			if matched == negative {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			if !negative {
				v.adoptCaptures(sub)
				v.matchStart = sub.matchStart
				v.mark = sub.mark
			}
			pc = v.prog.Inst[end].Out
		case OpLookbehindStart, OpNegLookbehindStart:
			negative := inst.Op == OpNegLookbehindStart
			end := v.lookbehindEndPC(inst.Out)
			max := inst.N2
			if max < 0 || max > v.pos {
				max = v.pos
			}
			pos, available := v.lookbehindStart(max)
			matched := false
			for length := available; length >= inst.N; length-- {
				if length < available {
					_, size := decodeCharacter(v.subject[pos:], v.flags)
					pos += size
				}
				sub, ok := v.runChild(inst.Out, end, pos, v.pos)
				if v.limitErr != nil {
					return false
				}
				if ok {
					matched = true
					if !negative {
						v.adoptCaptures(sub)
						v.mark = sub.mark
					}
					break
				}
			}
			if matched == negative {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			pc = v.prog.Inst[end].Out
		case OpAtomicStart:
			end := v.findAtomicEnd(pc + 1)
			sub, matched := v.runChild(pc+1, end, v.pos, -1)
			if v.limitErr != nil {
				return false
			}
			if !matched {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			v.pos, v.matchStart, v.mark = sub.pos, sub.matchStart, sub.mark
			v.adoptCaptures(sub)
			pc = v.prog.Inst[end].Out
		case OpLookaheadEnd, OpNegLookaheadEnd, OpLookbehindEnd, OpNegLookbehindEnd, OpAtomicEnd:
			pc = inst.Out

		case OpRecurse:
			if v.depth >= v.depthLimit {
				v.limitErr = ErrDepthLimit
				return false
			}
			savedCaps := make([]int, len(v.captures))
			copy(savedCaps, v.captures)
			v.stack.push(stackFrame{
				kind:     frameRecursion,
				pc:       inst.Out,
				captures: savedCaps,
				depth:    v.depth,
			})
			v.depth++
			pc = uint32(v.prog.Start)

		case OpSubroutineCall:
			if v.depth >= v.depthLimit {
				v.limitErr = ErrDepthLimit
				return false
			}
			groupIdx := inst.N
			entry, ok := v.prog.GroupEntry[groupIdx]
			if !ok {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			savedCaps := make([]int, len(v.captures))
			copy(savedCaps, v.captures)
			v.stack.push(stackFrame{
				kind:     frameRecursion,
				pc:       inst.Out,
				captures: savedCaps,
				depth:    v.depth,
			})
			v.depth++
			pc = uint32(entry)

		case OpAccept:
			if endPC < 0 && v.notEmptyAtStart && v.pos == v.matchStart && v.matchStart == v.searchStart {
				if !v.backtrack() {
					return false
				}
				pc = v.nextPC()
				continue
			}
			v.captures[0] = v.matchStart
			v.captures[1] = v.pos
			return true

		case OpVerbFail:
			if !v.backtrack() {
				return false
			}
			pc = v.nextPC()

		case OpCommit:
			v.stack.push(stackFrame{
				kind:     frameVerb,
				verbKind: VerbCommit,
			})
			pc = inst.Out

		case OpPrune:
			v.stack.push(stackFrame{
				kind:     frameVerb,
				verbKind: VerbPrune,
				pos:      v.pos,
			})
			pc = inst.Out

		case OpSkip:
			v.stack.push(stackFrame{
				kind:     frameVerb,
				verbKind: VerbSkip,
				pos:      v.pos,
			})
			pc = inst.Out

		case OpSkipName:
			v.stack.push(stackFrame{
				kind:     frameVerb,
				verbKind: VerbSkipName,
				markName: inst.Str,
			})
			pc = inst.Out

		case OpThen:
			v.stack.push(stackFrame{
				kind:     frameVerb,
				verbKind: VerbThen,
			})
			pc = inst.Out

		case OpMark:
			v.mark = inst.Str
			v.stack.push(stackFrame{
				kind:     frameMark,
				markName: inst.Str,
				markPos:  v.pos,
			})
			pc = inst.Out

		case OpCallout:
			if v.callout != nil {
				cb := &CalloutBlock{
					CalloutNumber:   inst.N,
					CalloutString:   inst.Str,
					Subject:         v.subject,
					CurrentPosition: v.pos,
					StartMatch:      v.matchStart,
					PatternPosition: int(pc),
				}
				result := v.callout(cb)
				if result != 0 {
					if !v.backtrack() {
						return false
					}
					pc = v.nextPC()
					continue
				}
			}
			pc = inst.Out

		case OpSetFlags:
			v.stack.push(stackFrame{
				kind:  frameFlagSave,
				flags: v.flags,
			})
			v.flags = Flag(inst.N) | (v.flags &^ Flag(inst.N2))
			pc = inst.Out

		default:
			// Unknown opcode — fail
			if !v.backtrack() {
				return false
			}
			pc = v.nextPC()
		}
	}
}

// nextPC returns the PC from the last popped frame (used after backtrack).
// This is a helper — the actual PC is stored in the vm after backtrack.
func (v *vm) nextPC() uint32 {
	return v.backtrackPC
}

// backtrack pops the stack looking for a split frame to resume from.
func (v *vm) backtrack() bool {
	for {
		f, ok := v.stack.pop()
		if !ok {
			return false
		}
		switch f.kind {
		case frameSplit:
			v.pos = f.pos
			v.backtrackPC = f.pc
			return true
		case frameCapture:
			v.captures[f.capSlot] = f.capVal
		case frameProgress:
			v.loopPositions[f.capSlot] = f.capVal
		case frameLookaround:
			v.pos = f.pos
			v.matchStart = f.matchStart
			if f.captures != nil {
				copy(v.captures, f.captures)
			}
		case frameAtomic:
			// Backtracking past an atomic group — continue popping
			continue
		case frameRecursion:
			v.depth = f.depth
			copy(v.captures, f.captures)
		case frameVerb:
			switch f.verbKind {
			case VerbCommit:
				// Fail the entire match
				return false
			case VerbPrune:
				// Advance start and fail this attempt
				return false
			case VerbSkip:
				return false
			case VerbThen:
				// Continue backtracking to next alternation split
				continue
			}
		case frameMark:
			if f.matchStart != 0 {
				v.matchStart = f.matchStart
			}
		case frameFlagSave:
			v.flags = f.flags
		}
	}
}

// saveCapturesForSplit saves capture state onto the stack so it can be
// restored if we backtrack to this split point.
func (v *vm) saveCapturesForSplit() {
	// We rely on individual capture save/restore frames rather than
	// snapshotting the entire capture array for performance.
}

func (v *vm) matchCharClass(r rune, inst *Inst) bool {
	matched := false
	runes := inst.Runes
	caseFold := v.flags&Caseless != 0

	// Check rune ranges (pairs: lo, hi, lo, hi, ...)
	for i := 0; i+1 < len(runes); i += 2 {
		lo, hi := runes[i], runes[i+1]
		if r >= lo && r <= hi {
			matched = true
			break
		}
		if caseFold {
			for fr := foldCharacter(r, v.flags); fr != r; fr = foldCharacter(fr, v.flags) {
				if fr >= lo && fr <= hi {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
	}

	for _, ct := range inst.CharTypes {
		if v.matchCharType(r, ct) {
			matched = true
			break
		}
	}

	// Check properties if present (stored as semicolon-separated names in Str)
	if !matched && inst.Str != "" {
		for _, propName := range splitProperties(inst.Str) {
			negate := false
			name := propName
			if len(name) > 0 && name[0] == '^' {
				negate = true
				name = name[1:]
			}
			m := matchProperty(r, name)
			if negate {
				m = !m
			}
			if m {
				matched = true
				break
			}
		}
	}

	if inst.Negate {
		return !matched
	}
	return matched
}

func splitProperties(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func (v *vm) matchCharType(r rune, ct CharTypeKind) bool {
	ucp := v.flags&UCP != 0
	switch ct {
	case CharTypeDigit:
		if ucp {
			return unicode.IsDigit(r)
		}
		return r >= '0' && r <= '9'
	case CharTypeNonDigit:
		if ucp {
			return !unicode.IsDigit(r)
		}
		return !(r >= '0' && r <= '9')
	case CharTypeWord:
		return isWordChar(r, ucp)
	case CharTypeNonWord:
		return !isWordChar(r, ucp)
	case CharTypeSpace:
		if ucp {
			return unicode.IsSpace(r)
		}
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
	case CharTypeNonSpace:
		if ucp {
			return !unicode.IsSpace(r)
		}
		return !(r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v')
	case CharTypeHSpace:
		return isHSpace(r)
	case CharTypeNonHSpace:
		return !isHSpace(r)
	case CharTypeVSpace:
		return isVSpace(r)
	case CharTypeNonVSpace:
		return !isVSpace(r)
	}
	return false
}

func (v *vm) atWordBoundary() bool {
	ucp := v.flags&UCP != 0
	prevWord := false
	nextWord := false
	if v.pos > 0 {
		r, _ := decodeLastCharacter(v.subject[:v.pos], v.flags)
		prevWord = isWordChar(r, ucp)
	}
	if v.pos < len(v.subject) {
		r, _ := decodeCharacter(v.subject[v.pos:], v.flags)
		nextWord = isWordChar(r, ucp)
	}
	return prevWord != nextWord
}

func (v *vm) matchStringFold(ref string) bool {
	pos := v.pos
	for i := 0; i < len(ref); {
		if pos >= len(v.subject) {
			return false
		}
		r1, size1 := decodeCharacter(ref[i:], v.flags)
		r2, size2 := decodeCharacter(v.subject[pos:], v.flags)
		if !equalFold(r1, r2, v.flags) {
			return false
		}
		i += size1
		pos += size2
	}
	v.pos = pos
	return true
}

// findLookaheadEnd scans instructions from startPC to find the matching lookahead end opcode.
func (v *vm) findLookaheadEnd(startPC uint32, endOp Opcode) int {
	depth := 0
	startOp := OpLookaheadStart
	if endOp == OpNegLookaheadEnd {
		startOp = OpNegLookaheadStart
	}
	for pc := startPC; int(pc) < len(v.prog.Inst); pc++ {
		op := v.prog.Inst[pc].Op
		if op == startOp {
			depth++
		} else if op == endOp {
			if depth == 0 {
				return int(pc)
			}
			depth--
		}
	}
	return int(startPC)
}

// findAtomicEnd scans instructions from startPC to find the matching OpAtomicEnd.
func (v *vm) findAtomicEnd(startPC uint32) int {
	depth := 0
	for pc := startPC; int(pc) < len(v.prog.Inst); pc++ {
		op := v.prog.Inst[pc].Op
		if op == OpAtomicStart {
			depth++
		} else if op == OpAtomicEnd {
			if depth == 0 {
				return int(pc)
			}
			depth--
		}
	}
	return int(startPC)
}

// One reusable child per active VM. Nested assertions use the child's own
// child, so reentry never resets a live invocation. Charge all work and heap
// against the enclosing invocation rather than copying its full allowance.
func (v *vm) runChild(start uint32, end, pos, target int) (*vm, bool) {
	if v.depth >= v.depthLimit {
		v.limitErr = ErrDepthLimit
		return nil, false
	}
	sub := newVM(v.prog, v.subject, pos)
	sub.resetAttempt(pos)
	sub.subject, sub.flags = v.subject, v.flags
	sub.matchStart, sub.startPos = v.matchStart, v.startPos
	sub.matchLimit = v.matchLimit - v.steps
	sub.depthLimit, sub.depth = v.depthLimit, v.depth+1
	sub.heapLimit = v.heapLimit - v.stack.heapUsed
	sub.callout, sub.mark = v.callout, v.mark
	sub.notEmptyAtStart = false
	copy(sub.captures, v.captures)
	matched := sub.execFrom(start, end, target)
	v.steps += sub.steps
	if sub.limitErr != nil {
		v.limitErr = sub.limitErr
	}
	return sub, matched
}

func (v *vm) lookbehindEndPC(start uint32) int {
	depth := 0
	for pc := int(start); pc < len(v.prog.Inst); pc++ {
		switch v.prog.Inst[pc].Op {
		case OpLookbehindStart, OpNegLookbehindStart:
			depth++
		case OpLookbehindEnd, OpNegLookbehindEnd:
			if depth == 0 {
				return pc
			}
			depth--
		}
	}
	return len(v.prog.Inst) - 1
}

// Helper functions

func equalFold(r1, r2 rune, flags Flag) bool {
	if r1 == r2 {
		return true
	}
	// Use unicode.SimpleFold to iterate through the case-fold orbit
	for fr := foldCharacter(r1, flags); fr != r1; fr = foldCharacter(fr, flags) {
		if fr == r2 {
			return true
		}
	}
	return false
}

func isHSpace(r rune) bool {
	switch r {
	case '\t', ' ', 0xA0, 0x1680, 0x180E,
		0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200A,
		0x202F, 0x205F, 0x3000:
		return true
	}
	return false
}

func isVSpace(r rune) bool {
	switch r {
	case '\n', '\v', '\f', '\r', 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

func matchProperty(r rune, name string) bool {
	tab := resolveProperty(name)
	if tab != nil {
		return unicode.Is(tab, r)
	}
	return false
}

// Move back by characters rather than adjusting a byte counter. Each retry
// advances one complete character, including at a multibyte subject's end.
func (v *vm) lookbehindStart(max int) (int, int) {
	pos, count := v.pos, 0
	for count < max && pos > 0 {
		_, size := decodeLastCharacter(v.subject[:pos], v.flags)
		pos -= size
		count++
	}
	return pos, count
}

// Assertion/atomic success exports captures with an undo record in the parent.
func (v *vm) adoptCaptures(sub *vm) {
	for i, value := range sub.captures {
		if value == v.captures[i] {
			continue
		}
		v.stack.push(stackFrame{kind: frameCapture, capSlot: i, capVal: v.captures[i]})
		v.captures[i] = value
	}
}
