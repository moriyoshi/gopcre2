# Native Laravel matching fixtures

The two compressed fixtures normalize 1,055 Eloquent and 130 JSON matching
calls captured by php2go. They preserve native PHP 8.5.11 / PCRE2 10.44 capture
offsets through the last participating group, including unset holes.

The checked-in fixtures contain the inputs and expected byte offsets needed
for Go validation and replay. `internal/replay` supplies reusable loading,
validation, global search and instruction-counting helpers.

Engine entry points and replay executables are introduced in later branches.
The PCRE2 baseline harness already runs against the original interpreter.
