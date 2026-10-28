package gopcre2

// Matcher owns reusable interpreter workspace. It is not safe for concurrent
// use. Find returns borrowed offsets, valid until its next call.
type Matcher struct {
	re *Regexp
	vm *vm
}

func (re *Regexp) NewMatcher() *Matcher { return &Matcher{re, re.newMatchVM("", 0)} }
func (m *Matcher) Find(subject string, start int, anchored, notEmptyAtStart bool) ([]int, error) {
	m.re.prepareVM(m.vm, subject, start)
	defer clearSubjects(m.vm)
	ok, err := m.re.searchVM(m.vm, subject, start, anchored, notEmptyAtStart, false)
	if !ok {
		return nil, err
	}
	return m.vm.captures, nil
}

// MatchPHP performs one PCRE2-style search, with per-call anchoring and
// NOTEMPTY_ATSTART semantics. Captures are owned by the caller; limits and
// invalid UTF/offset errors are observable. Lookbehind sees the full subject.
func (re *Regexp) MatchPHP(subject string, start int, anchored, notEmptyAtStart bool) ([]int, error) {
	v := re.acquireVM(subject, start)
	defer re.releaseVM(v)
	ok, err := re.searchVM(v, subject, start, anchored, notEmptyAtStart, false)
	if !ok {
		return nil, err
	}
	return append([]int(nil), v.captures...), nil
}

func (re *Regexp) prepareVM(v *vm, subject string, start int) {
	v.subject = subject
	v.resetAttempt(start)
	v.startPos = start
	limit := func(api, inline, def int) int {
		n := def
		if api > 0 {
			n = api
		}
		if inline > 0 && inline < n {
			n = inline
		}
		return n
	}
	v.matchLimit = limit(re.matchLimit, re.prog.MatchLimit, DefaultMatchLimit)
	v.depthLimit = limit(re.depthLimit, re.prog.DepthLimit, DefaultDepthLimit)
	v.heapLimit = limit(re.heapLimit, re.prog.HeapLimit, DefaultHeapLimit)
	v.callout = re.callout
}
func (re *Regexp) acquireVM(subject string, start int) *vm {
	v := newVM(re.prog, subject, start)
	re.prepareVM(v, subject, start)
	return v
}
func clearSubjects(v *vm) {
	for ; v != nil; v = v.child {
		v.subject = ""
		v.callout = nil
		v.stack.truncate(0)
	}
}
func (re *Regexp) releaseVM(v *vm) {
	if v != nil {
		clearSubjects(v)
	}
}

// After an empty result, retry this offset with ANCHORED|NOTEMPTY_ATSTART;
// only advance a character after that retry fails. Each result is emitted once.
type matchIterator struct {
	re             *Regexp
	v              *vm
	subject        string
	offset         int
	notEmpty, done bool
	errorAware     bool
	err            error
}

func (re *Regexp) iterate(subject string, errorAware bool) *matchIterator {
	return &matchIterator{re: re, v: re.acquireVM(subject, 0), subject: subject, errorAware: errorAware}
}
func (it *matchIterator) close() { it.re.releaseVM(it.v) }
func (it *matchIterator) next() *vm {
	for !it.done {
		ok, err := it.re.searchVM(it.v, it.subject, it.offset, it.notEmpty, it.notEmpty, !it.errorAware)
		if err != nil {
			it.err = err
			it.done = true
			return nil
		}
		if !ok {
			if it.notEmpty && it.offset < len(it.subject) {
				_, size := decodeCharacter(it.subject[it.offset:], it.re.flags)
				it.offset += size
				it.notEmpty = false
				continue
			}
			it.done = true
			return nil
		}
		it.offset = it.v.captures[1]
		it.notEmpty = it.v.captures[0] == it.offset
		return it.v
	}
	return nil
}
