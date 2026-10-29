# PCRE2 encoding fixtures

`encoding.json.gz` contains 12,375 binary-safe cases generated from PCRE2 10.49,
commit `6f9d7c1373262c541324a16a358785b33ef116cf`. Unlike the upstream JIT
regression table's NUL-terminated strings, these cases preserve embedded NULs
through explicit lengths. Patterns and subjects are hex encoded. Captures come
from `pcre2_match(..., PCRE2_NO_JIT)` and are checked against the general API
with JIT enabled before the fixture is written. Invalid UTF subjects use the
general API's UTF validation; direct JIT does not validate them.

Coverage includes every byte under plain, UCP, caseless and UCP/caseless options;
positive and negative shorthand classes; literal and range folding; boundaries;
backreferences; byte versus UTF quantifiers; escaped range endpoints; embedded
NULs; malformed UTF; out-of-range escapes; and misplaced encoding directives.
The base tests check all cases. The optional JIT tests check its supported cases
three times on each reusable matcher and report explicit fallback counts.

Build the pinned source as described in [the benchmark report](../benchmarks/pcre2/README.md),
then regenerate from the repository root:

```sh
cc -O3 -Wall -Wextra -Werror -I /tmp/gopcre2-pcre2-10.49/build/interface \
  benchmarks/pcre2/native/oracle.c \
  /tmp/gopcre2-pcre2-10.49/build/libpcre2-8.a -o /tmp/gopcre2-encoding-oracle
python3 benchmarks/pcre2/encoding.py --oracle /tmp/gopcre2-encoding-oracle
go test -run 'TestPCRE2Encoding|TestEncodingIteration' -v
```

These are generated test cases authored for this implementation, separately
from the upstream regression corpus retained under its BSD notice.
