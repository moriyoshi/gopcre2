// Package jit provides an experimental, optional nyanjit compiler for gopcre2.
// The base module has no native-code dependency. Unsupported expressions retain
// the interpreter; NativeError reports why compilation was declined.
package jit

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	p "github.com/KarpelesLab/gopcre2"
	"github.com/moriyoshi/nyanjit/backend/execution"
)

var (
	ErrUnsupported = errors.New("gopcre2/jit: unsupported pattern")
	ErrSubject     = errors.New("gopcre2/jit: native UTF matcher currently requires ASCII input")
)

// Regexp implements the interpreter API. MatchString and FindStringSubmatchIndex
// use native code when supported; other matching methods use the interpreter.
// Configure limits before concurrent matching. Close is safe during matching.
type Regexp struct {
	base                  *p.Regexp
	code                  *execution.Code
	owner                 *codeOwner
	closed                atomic.Bool
	err                   error
	words                 int
	pool                  sync.Pool
	matchLimit, heapLimit int
}

// Compile retains the interpreter when native compilation is unavailable.
// Syntax errors from gopcre2 still return an error.
func Compile(pattern string, flags ...p.Flag) (*Regexp, error) {
	base, err := p.Compile(pattern, flags...)
	if err != nil {
		return nil, err
	}
	r := &Regexp{base: base, matchLimit: p.DefaultMatchLimit, heapLimit: p.DefaultHeapLimit}
	r.code, r.words, r.err = compileNative(base.Pattern())
	r.pool.New = func() any { return r.NewMatcher() }
	if r.code != nil {
		r.owner = &codeOwner{r.code}
		runtime.SetFinalizer(r.owner, func(o *codeOwner) { o.code.Close() })
	}
	return r, nil
}

// NativeError explains why a pattern did not receive native code.
func (r *Regexp) NativeError() error { return r.err }

// Native reports that native code was compiled. A non-ASCII invocation can
// still fall back; Matcher.Find makes that distinction explicit per call.
func (r *Regexp) Native() bool { return r.code != nil }

type codeOwner struct{ code *execution.Code }

func (r *Regexp) Close() error {
	r.closed.Store(true)
	if r.code != nil {
		return r.code.Close()
	}
	return nil
}

func (r *Regexp) SetMatchLimit(n int) *Regexp {
	r.base.SetMatchLimit(n)
	r.matchLimit = n
	if n <= 0 {
		r.matchLimit = p.DefaultMatchLimit
	}
	return r
}
func (r *Regexp) SetHeapLimit(n int) *Regexp {
	r.base.SetHeapLimit(n)
	r.heapLimit = n
	if n <= 0 {
		r.heapLimit = p.DefaultHeapLimit
	}
	return r
}

// state contains only transferable scalar/slice fields. Matcher retains this
// heap-resident object and its current backing arrays throughout each call.
type state struct {
	Input           []byte
	Captures        []int
	Scratch         []uintptr
	Start           uintptr
	Limit           uintptr
	Anchored        uintptr
	NotEmptyAtStart uintptr
}

// Matcher owns reusable output and scratch. It is not safe for concurrent use.
// Find returns borrowed offsets, valid until the next call on this Matcher.
// Native-only operation makes fallback and limit failures observable.
type Matcher struct {
	re    *Regexp
	state *state
	entry func(*state) uintptr
	lease *execution.FuncLease
	err   error
}

func (r *Regexp) NewMatcher() *Matcher {
	s := &state{Captures: make([]int, 2*(r.NumSubexp()+1)), Scratch: make([]uintptr, r.words)}
	m := &Matcher{re: r, state: s}
	if r.code != nil {
		m.lease, m.err = r.code.LeafFunc(&m.entry)
	}
	return m
}

func (m *Matcher) Find(subject string, start int) ([]int, error) {
	return m.FindWithOptions(subject, start, false, false)
}

// FindWithOptions performs one native search with PCRE2's per-call options.
func (m *Matcher) FindWithOptions(subject string, start int, anchored, notEmptyAtStart bool) ([]int, error) {
	r := m.re
	if r.closed.Load() {
		return nil, execution.ErrClosed
	}
	if r.code == nil {
		return nil, r.err
	}
	if m.err != nil {
		return nil, m.err
	}
	if start < 0 || start > len(subject) {
		return nil, nil
	}
	// Byte mode accepts arbitrary bytes, including malformed UTF-8. UTF
	// matching remains an explicitly guarded subset until decoding is lowered.
	if r.Pattern().Flags()&p.UTF != 0 {
		for i := range len(subject) {
			if subject[i] >= 128 {
				return nil, ErrSubject
			}
		}
	}

	if len(subject)-start < r.Pattern().MinBytes() {
		return nil, nil
	}
	if r.words*8 > r.heapLimit {
		return nil, p.ErrHeapLimit
	}
	s := m.state
	s.Input = unsafe.Slice(unsafe.StringData(subject), len(subject))
	s.Start = uintptr(start)
	s.Anchored, s.NotEmptyAtStart = 0, 0
	if anchored {
		s.Anchored = 1
	}
	if notEmptyAtStart {
		s.NotEmptyAtStart = 1
	}
	s.Limit = uintptr(r.matchLimit)
	for i := range s.Captures {
		s.Captures[i] = -1
	}
	// The typed state pointer is non-nil and heap-resident. KeepAlive(m)
	// retains both the lease and all typed argument roots through the leaf
	// call. Updating Input uses Go's write barrier; generated code performs
	// no callbacks, pointer stores, slice replacement, or allocation.
	status := m.entry(s)
	s.Input = nil
	runtime.KeepAlive(subject)
	runtime.KeepAlive(m)
	switch status {
	case 0:
		return nil, nil
	case 1:
		return s.Captures, nil
	case 2:
		return nil, p.ErrMatchLimit
	default:
		return nil, errors.New("gopcre2/jit: invalid native status")
	}
}

func (r *Regexp) MatchString(subject string) bool {
	if r.code == nil {
		return r.base.MatchString(subject)
	}
	m := r.pool.Get().(*Matcher)
	got, err := m.Find(subject, 0)
	r.pool.Put(m)
	if errors.Is(err, ErrSubject) {
		return r.base.MatchString(subject)
	}
	return err == nil && got != nil
}

func (r *Regexp) FindStringSubmatchIndex(subject string) []int {
	if r.code == nil {
		return r.base.FindStringSubmatchIndex(subject)
	}
	m := r.pool.Get().(*Matcher)
	got, err := m.Find(subject, 0)
	var result []int
	if err == nil && got != nil {
		result = append([]int(nil), got...)
	}
	r.pool.Put(m)
	if errors.Is(err, ErrSubject) {
		return r.base.FindStringSubmatchIndex(subject)
	}
	return result
}

// MatchPHP mirrors the interpreter's error-aware, owned-output entry point.
func (r *Regexp) MatchPHP(subject string, start int, anchored, notEmptyAtStart bool) ([]int, error) {
	if r.closed.Load() {
		return nil, execution.ErrClosed
	}
	if r.code == nil {
		return r.base.MatchPHP(subject, start, anchored, notEmptyAtStart)
	}
	m := r.pool.Get().(*Matcher)
	got, err := m.FindWithOptions(subject, start, anchored, notEmptyAtStart)
	var result []int
	if err == nil && got != nil {
		result = append([]int(nil), got...)
	}
	r.pool.Put(m)
	if errors.Is(err, ErrSubject) {
		return r.base.MatchPHP(subject, start, anchored, notEmptyAtStart)
	}
	return result, err
}
