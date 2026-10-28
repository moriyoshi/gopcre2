# php2go handoff follow-up, 2026-10-29

The seven fidelity defects in the coordinator's handoff are fixed. Every
normalized matching call from the two Laravel captures agrees with native
PHP 8.5.11 / PCRE2 10.44, including normalized capture vectors. The php2go tree
was read only throughout this work. No commits were made.

## Updated fidelity table

These expectations are the handoff's native PCRE2 answers. Regression tests
are in [fidelity_handoff_test.go](../../fidelity_handoff_test.go).

| ID | Case | gopcre2 now |
| --- | --- | --- |
| F7 | `(?<!ක)`, UTF/UCP, subject `ම` | Terminates; global offsets `[[0,0],[3,3]]`; substitution `*ම*` |
| F1 | `(?<q>["'])(.*?)\k<q>`, subject `say "hi" now` | `[4,8,4,5,5,7]` |
| F2 | `(?<=(a))b`, subject `ab` | `[1,2,0,1]`; captures survive and undo correctly |
| F3 | `^$`, Multiline, subject `a\n` | No match |
| F4 | `a{,2}`, subject `a{,2}` | `[0,1]` |
| F5 | `(?U)<.*>`, subject `<aa> <bb> <cc>` | `[0,4]`; scoped ungreedy options also work |
| F6 | `(?P<1>blub)` | Compile error; the other named-group spellings reject digit-led names too |

The hang fix walks complete characters backwards, then advances monotonically
between retries. The global iterator emits each empty result once, retries the
same offset with ANCHORED/NOTEMPTY_ATSTART, and advances a character only after
that retry fails. End-of-subject empty replacements preserve the subject once.
Named references resolve declared names and choose the first participating
duplicate group. Relative numeric `\g{-1}` references are also resolved.

## Workspace and search changes

Each interpreter VM owns one reusable child; nested assertions use that child's
own child. Lookbehind, lookahead and atomic groups use the same dispatcher as
the main match. The old sub-dispatcher skipped some opcodes. Successful capture
changes enter the parent's undo stack. A failed negative assertion cannot turn
a child limit error into success.

The child's work is charged to its parent's current attempt; its heap allowance
subtracts the parent's active frames. PCRE2 resets the match counter for each
candidate start, as implemented in `src/pcre2_match.c`'s bumpalong loop. The
Go VM counts dispatched instructions, so its numeric budget units differ from
native PCRE2's match-function calls.

Top-level workspaces live in a `sync.Pool` on their Regexp. Released workspaces
clear subjects and callbacks throughout the child chain. Workspaces exceeding
1 MiB of retained arrays are not returned to the pool. Concurrent matching and
callback reentry use separate active VMs; there is no global map retaining
Regexps.

The shared structured pattern now carries conservative FIRST/nullable and
anchoring proofs. They support a singleton `IndexByte` scan, multi-byte-set
scans, and alternative pruning in both engines. Lookarounds are transparent to
the consuming FIRST set. UTF literals include folded lead bytes; uncertain
constructs give up conservatively. Nullable patterns keep their empty matches.
`NoStartOptimize` disables the new filters. Implicit anchoring recognizes every
branch beginning with `\A`, non-multiline `^`, or `\G`.

## Consumer entry points

Both modules provide:

```go
offsets, err := re.MatchPHP(subject, start, anchored, notEmptyAtStart)
```

The returned offsets are owned by the caller. Lookbehind sees the full subject
before `start`. Limits, invalid UTF, continuation-byte offsets and inverted
match ranges are observable errors. Configure limits before concurrent use.
The legacy index/boolean APIs retain their existing behavior of trying later
candidates after an exhausted attempt; `MatchPHP` stops and returns the error.

For borrowed output, the interpreter exposes
`re.NewMatcher().Find(subject, start, anchored, notEmptyAtStart)`. The JIT's
equivalent is `re.NewMatcher().FindWithOptions(...)`; its existing two-argument
`Find` uses default options. Each matcher is private to one concurrent caller.
Warm interpreter lookaround matching has zero allocations in the reuse tests.

## JIT coverage

Top-level lookbehind alternatives now step back by their own fixed widths,
including zero-width branches such as `^`. Assertion success remains atomic;
capture rollback is preserved. Exact counted repeats can form deterministic
units, including the paired-backslash patterns. Flattening is bounded by the
lowering budget. Native state also carries per-call Anchored/NotEmptyAtStart.

Native compilation covers **1,053/1,055 Eloquent calls** and **128/130 JSON
calls**. These counts describe compiled patterns; the native-only matcher still
declines non-ASCII UTF subjects explicitly. Large UTF character classes can
compile for ASCII subjects, with other UTF input using the interpreter. This
does not implement native UTF decoding. The remaining refused calls use the
multiline extended pattern with general unbounded group repetition.

## Measurements and reproduction

Measurements cover the regex matching and global-search work extracted from
the requests, including output-vector copies. PHP callback execution, final
replacement/split formatting and full Laravel request execution are outside
these timings. The corpus contains 1,055 Eloquent calls over 40 patterns and
130 JSON calls over 29 patterns. Both stages use the same matching units and
native capture-vector oracle, with 259 and 35 matches per pass respectively.
Normalization retains every participating capture and unset hole through the
last participating group. The separate upstream PCRE2 corpus also compares
all declared trailing-unset groups. Captured UTF subjects in this workload
are ASCII; native UTF execution on other subjects remains explicitly guarded.

See [saved samples, medians and source snapshots](results/2026-10-29-arm64).

| Route / stage | Instructions/pass | Time/pass | Allocations/pass | Bytes/pass |
| --- | ---: | ---: | ---: | ---: |
| eloquent / before | 788.408 M | 73,099.59 μs | 212,566.00 | 507,050,160 |
| eloquent / pooled | 32.219 M | 1,892.97 μs | 774.47 | 25,165 |
| eloquent / filtered | 5.149 M | 293.79 μs | 775.33 | 26,510 |
| eloquent / jit | 1.635 M | 87.36 μs | 774.71 | 25,196 |
| json / before | 2.066 M | 161.27 μs | 536.00 | 1,015,104 |
| json / pooled | 0.572 M | 37.99 μs | 104.17 | 2,693 |
| json / filtered | 0.532 M | 33.58 μs | 104.17 | 2,693 |
| json / jit | 0.165 M | 8.77 μs | 104.17 | 2,693 |

Eloquent matching work is 837× faster by elapsed time and uses 482× fewer
instructions with JIT than the pre-handoff interpreter. JSON matching work is
18.4× faster. FIRST/anchor/alternative filters remove about 84% of the pooled
interpreter's Eloquent instructions. These ratios use the fixed native-validated
corpus, not a reduced supported-only workload; the four refused pattern calls
remain in the replay through interpreter fallback.

Each sample performs 200 passes; three samples per stage use CPU 6,
GOMAXPROCS=1 and enabled GC in a 2 GiB, network-disabled container with only
the additional PERFMON capability. Compilation, fixture loading and full native
validation precede timing. The instruction counter excludes kernel/hypervisor
work. CPU 2 returned zero counter values and was excluded from measurements.
All reported measurements select CPU 6. One diagnostic timing recheck of
case 394 was mistakenly launched without affinity; its timing is excluded.
Subsequent checks were pinned to CPUs 0–4. Available memory was checked before
replay.

```sh
CGO_ENABLED=0 go build -o /tmp/gopcre2-replay ./benchmarks/laravel/replay
python3 benchmarks/laravel/measure.py --binary /tmp/gopcre2-replay \
  --stage filtered --output /tmp/gopcre2-replay-results --cpu 6
CGO_ENABLED=0 GOWORK=off go -C jit build -o /tmp/gopcre2-jit-replay ./cmd/laravel
python3 benchmarks/laravel/measure.py --binary /tmp/gopcre2-jit-replay \
  --stage jit --output /tmp/gopcre2-replay-results --cpu 6
```

The checked-in fixtures preserve normalized inputs and expected native byte
offsets from the captured calls. Leading whitespace before a PHP delimiter
was handled during normalization. Go validation and replay read those fixtures
directly. The before-source archive includes the match-option adapter needed
to run the same replay on the preserved interpreter.

The separate [PCRE2 corpus comparison](../pcre2/results/2026-10-29-handoff-core)
validates all 633 eligible upstream cases. Correct vectors improve from 440
to **464**, with no regression among those 440. There remain 125 differing
results and 44 parser rejections. Three 3 ms-minimum samples per case on that
fixed cohort give **5.96× geometric-mean interpreter speedup**, median latency
1,014 ns to 203 ns, and median allocations five to one. Case 394 in the old
engine allocates about 9.3 GB cumulatively per match; timing-worker deadlines
allow its validation and all repetitions to finish, while sample settings stay
identical between workers.

## Additional PHPT triage and remaining work

- Inverted `\K` matches in lookahead now return `ErrInvalidMatch` through
  `MatchPHP`, with a regression test. The PHP adapter can turn that into its
  required warning/error rather than slicing an invalid range.
- Duplicate names remain present at every group index in `SubexpNames` and
  in numeric capture vectors. PHP's named result construction must select a
  participating group; blindly overwriting a name with an unset later group
  remains an adapter bug. Named backreferences now make that selection.
- MARK values exist inside the VM, but the current public API does not expose
  them. A result API and PHP adapter support are still needed for `marks.phpt`.
- `gh16189` tests PHP_INT_MIN offset validation and PHP's ValueError contract.
  PHP's adapter must validate it before conversion; the Go entry point rejects
  out-of-range starts without indexing the subject.
- Conditionals (`bug76514`) and recursive replacement patterns remain separate
  conformance work; this batch does not certify those PHPTs.

Race checks pass in both modules and the backend execution package; the JIT
also passes with cgo disabled and compiles for Linux/amd64. Executed native
tests and timings are arm64. On the PCRE2 10.49 JIT corpus, 177 native-supported
cases pass: reusable matching is 1.12× native JIT time geometrically, p90
1.72×, with zero measured allocations. The allocating public API is 3.07×
native direct-JIT time. [Raw PCRE2 results](../pcre2/results/2026-10-29-arm64-jit)
retain coverage and source identity.

Next JIT coverage work is native UTF-8 decoding with character-width-aware
iterators, Unicode tables and capture restoration, followed by general repeated
groups with a dynamic backtracking stack. The bitmap-vs-range-class experiment
and wider PHPT replay remain open. Current passing subsets do not establish
whole-library PCRE2 equivalence.
