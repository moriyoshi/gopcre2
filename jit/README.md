# Experimental nyanjit compiler

This companion module implements PCRE2-style matching and backtracking code
generation using the sibling `../../nyanjit` checkout. The base gopcre2 module
keeps its Go 1.25.3 requirement and dependency-free interpreter. This module
requires Go 1.26 or newer and is currently a development checkout, using local
module replacements until nyanjit and its backend are published together.
It also requires the new `LeafFunc`/`FuncLease` extension in
[`nyanjit/backend/execution`](../../nyanjit/backend/execution/leaf_func.go),
provided as two new files in the sibling checkout by this implementation.

```go
package main

import (
	"fmt"
	"github.com/KarpelesLab/gopcre2/jit"
)

func main() {
	r, err := jit.Compile(`(ab|a)b`)
	if err != nil {
		panic(err)
	}
	defer r.Close()

	fmt.Println(r.MatchString("ab"))             // true
	fmt.Println(r.FindStringSubmatchIndex("ab")) // [0 2 0 1]

	if r.Native() {
		matcher := r.NewMatcher()
		offsets, err := matcher.Find("ab", 0)
		// offsets borrows the matcher's output until its next Find call.
		fmt.Println(offsets, err)
	} else {
		fmt.Println(r.NativeError())
	}
}
```

`MatchString`, `FindStringSubmatchIndex` and `MatchPHP` use native code for supported
patterns over arbitrary byte subjects. In UTF mode, the current native subset
requires ASCII subjects; other subjects use the interpreter. Unsupported
patterns also use the interpreter. Other matching, replacement, and iteration APIs currently
delegate to the interpreter. The interpreter's existing compatibility gaps
also apply to those fallbacks; fallback does not certify PCRE2 equivalence.

`NewMatcher().Find` is an error-aware, native-only API: unsupported patterns or
subjects are reported explicitly, as are work/heap limits and closed code. A
Matcher owns reusable captures, numeric scratch and prepared rooted arguments;
warm calls allocate no output or scratch. A Matcher is not safe for concurrent
use. The Regexp's native public methods use separate pooled matchers and
are safe to call concurrently after configuration.

The current native subset includes:

- Byte literals/classes/types, dot, anchors, word boundaries and scoped options.
- Latin-1 UCP membership and byte-mode caseless matching, including backreferences.
- Composite classes with positive or negative shorthand predicates.
- Alternation and named/numbered capture declarations, including zero repeats.
- Greedy, lazy and possessive repeats of deterministic fixed-width units,
  including captured units such as `(ab)+` and exact counted inner units.
- Generic finite repeats up to eight copies, within the lowering budget.
- Atomic groups, lookahead and fixed-width top-level lookbehind branches,
  including branches with different lengths and zero-width anchors.
- Numeric backreferences, including a repeated capture's self-reference.
- FIRST-set candidate scans, implicit anchoring, per-alternative pruning,
  and a required literal at a proven fixed offset.
- Per-call Anchored and NotEmptyAtStart through `MatchPHP` and
  `Matcher.FindWithOptions`.

UTF decoding, property escapes, general unbounded group repetition, variable
length within an individual lookbehind branch, named backreferences, recursion, conditions, control verbs and
callouts remain interpreter operations. Codegen currently targets Linux/amd64
and Linux/arm64; unsupported targets keep the interpreter. The saved native
measurements are arm64 only.

The compiler retains repeats and scopes in the base module's immutable Pattern
representation. Forward matching blocks precede restoration blocks, which are
emitted in reverse order. Native branches resume known continuations; there is
no runtime opcode dispatch loop. Static scratch holds only numeric positions,
capture snapshots and iterator bounds. A deterministic repeat does not push a
frame for every input character. The preflight bounds expanded lowering work
to 512 expression nodes; this budget does not replace the parser/VM compiler's
own resource behavior.

The entry uses the sibling backend's new `Code.LeafFunc`, acquiring a mapping
lease once per Matcher. The typed entry has no reflection wrapper or per-call
gateway. The state pointer is non-nil and heap-resident, and Go installs its
input slice using the write barrier. `runtime.KeepAlive(matcher)` after entry
retains the lease, state and all argument backing arrays throughout execution.
Generated code makes no callbacks or pointer stores. `LeafFunc` rejects managed
entries and documents this trusted leaf/rooting contract explicitly.

Each lease owns one existing Code call reference, released by its finalizer;
there is no manual release that could race an active invocation. Code closure
blocks new leases; existing mappings remain valid until their leases disappear.
Regexp closure also prevents new matcher invocations. A separate code owner
finalizes closure when all Regexp/Matcher owners disappear. This avoids
`Code.Func`'s permanent mapping quarantine. The backend tests verify both
execution after Code.Close under a live lease and reclamation after lease GC.

`SetMatchLimit` bounds generated matching/search work. Its units differ from
the interpreter's VM instruction budget. `SetHeapLimit` bounds native scratch;
native recursion is not supported, so depth limits affect the interpreter.
Configure limits before concurrent matching. A native limit failure is not
retried through the interpreter.

Run both modules' checks from the repository root:

```sh
go test -race ./...
cd jit
GOWORK=off go test -race ./...
CGO_ENABLED=0 GOWORK=off go test ./...
```

`TestPCRE2Corpus` compares every native-supported eligible case against the
saved PCRE2 10.49 capture vectors. It also reports unsupported patterns,
non-ASCII UTF subjects and parser rejections. The binary-safe encoding suite
adds 12,375 fixtures, including every byte under plain, UCP, caseless and
UCP/caseless options, NULs, malformed UTF and invalid pattern values. Additional tests exercise capture
rollback, self-reference, empty-repeat progress, start offsets, concurrent
execution, code closure, GC and zero-allocation reuse. The regular subset is
also checked over 2,032 small pattern/subject combinations against Go regexp.

The [benchmark report and implementation plan](../benchmarks/pcre2/README.md)
describe the original baseline. To compare this implementation, first build
the native adapter with that report's `run.py` instructions, then run:

```sh
python3 benchmarks/pcre2/compare.py --native /tmp/gopcre2-baseline/native \
  --output /tmp/gopcre2-jit-comparison --cpu 7 --samples 7 --benchtime 20ms
```

Choose an available CPU on your machine. The script validates all 633 eligible
interpreter cases, checks the original timing cohort for regressions, measures
both reusable and public native matching in one process per case, then measures
native PCRE2 and the updated interpreter sequentially on the same core. Pass
`--shared-process` to retain all JIT cases in one process and inspect sensitivity
to that context. It reports the exact
intersection with the original timing cohort so coverage changes cannot
manufacture a speedup.

The benchmark also runs a prepared-argument `Code.Call` control using the same
compiled code and host checks. This isolates entry overhead without changing
nyanjit versions. The runner snapshots gopcre2 and archives a committed nyanjit
revision, then overlays only the two named leaf-entry extension files. Pass
`--nyanjit-ref <commit>` to select the dependency revision. Both workers build
against these immutable copies, allowing independent checkout edits during
measurement. Metadata records the commit, source hashes and snapshot paths.
Use `export_comparison.py <run-directory> <results-directory>` to save compact
raw samples and per-case CSV tables.

The [2026-10-29 php2go handoff follow-up](../benchmarks/laravel/README.md)
adds a native-validated Laravel replay, interpreter workspace reuse and
per-call matching options. It reports the seven fidelity fixes and the
remaining PHP adapter and engine gaps separately.

The companion's required backend extension is included for review as
[`nyanjit-leaf-entry.patch`](nyanjit-leaf-entry.patch). It adds only the leased
entry implementation and its lifetime tests to nyanjit revision
`29aa5790dc7ccd5510dea0d79fafe8ccdb5347db`. Apply it to a clean sibling checkout
before testing this optional module. Unrelated nyanjit working changes are
not part of this PR.
