package jit

import p "github.com/KarpelesLab/gopcre2"

// Callers cannot replace the private interpreter after native code has
// committed to its capture/scratch layout.
func (r *Regexp) String() string              { return r.base.String() }
func (r *Regexp) NumSubexp() int              { return r.base.NumSubexp() }
func (r *Regexp) SubexpNames() []string       { return r.base.SubexpNames() }
func (r *Regexp) SubexpIndex(name string) int { return r.base.SubexpIndex(name) }
func (r *Regexp) Pattern() *p.Pattern         { return r.base.Pattern() }
func (r *Regexp) Dump() string                { return r.base.Dump() }
func (r *Regexp) SetDepthLimit(n int) *Regexp { r.base.SetDepthLimit(n); return r }
func (r *Regexp) SetCallout(fn p.CalloutFunc) { r.base.SetCallout(fn) }

// These APIs retain their interpreter behavior during the initial JIT rollout.
func (r *Regexp) Match(b []byte) bool                        { return r.base.Match(b) }
func (r *Regexp) FindString(s string) string                 { return r.base.FindString(s) }
func (r *Regexp) Find(b []byte) []byte                       { return r.base.Find(b) }
func (r *Regexp) FindStringIndex(s string) []int             { return r.base.FindStringIndex(s) }
func (r *Regexp) FindIndex(b []byte) []int                   { return r.base.FindIndex(b) }
func (r *Regexp) FindStringSubmatch(s string) []string       { return r.base.FindStringSubmatch(s) }
func (r *Regexp) FindSubmatch(b []byte) [][]byte             { return r.base.FindSubmatch(b) }
func (r *Regexp) FindSubmatchIndex(b []byte) []int           { return r.base.FindSubmatchIndex(b) }
func (r *Regexp) FindAllString(s string, n int) []string     { return r.base.FindAllString(s, n) }
func (r *Regexp) FindAll(b []byte, n int) [][]byte           { return r.base.FindAll(b, n) }
func (r *Regexp) FindAllStringIndex(s string, n int) [][]int { return r.base.FindAllStringIndex(s, n) }
func (r *Regexp) FindAllIndex(b []byte, n int) [][]int       { return r.base.FindAllIndex(b, n) }
func (r *Regexp) FindAllStringSubmatch(s string, n int) [][]string {
	return r.base.FindAllStringSubmatch(s, n)
}
func (r *Regexp) FindAllSubmatch(b []byte, n int) [][][]byte { return r.base.FindAllSubmatch(b, n) }
func (r *Regexp) FindAllStringSubmatchIndex(s string, n int) [][]int {
	return r.base.FindAllStringSubmatchIndex(s, n)
}
func (r *Regexp) FindAllSubmatchIndex(b []byte, n int) [][]int {
	return r.base.FindAllSubmatchIndex(b, n)
}
func (r *Regexp) ReplaceAllString(s, repl string) string { return r.base.ReplaceAllString(s, repl) }
func (r *Regexp) ReplaceAll(s, repl []byte) []byte       { return r.base.ReplaceAll(s, repl) }
func (r *Regexp) ReplaceAllStringFunc(s string, repl func(string) string) string {
	return r.base.ReplaceAllStringFunc(s, repl)
}
func (r *Regexp) ReplaceAllFunc(s []byte, repl func([]byte) []byte) []byte {
	return r.base.ReplaceAllFunc(s, repl)
}
func (r *Regexp) Split(s string, n int) []string               { return r.base.Split(s, n) }
func (r *Regexp) Substitute(s, repl string) (string, error)    { return r.base.Substitute(s, repl) }
func (r *Regexp) SubstituteAll(s, repl string) (string, error) { return r.base.SubstituteAll(s, repl) }
