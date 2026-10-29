/* Benchmark adapter; corpus.h is extracted verbatim from upstream at run time. */
#define _POSIX_C_SOURCE 200809L
#define PCRE2_CODE_UNIT_WIDTH 8
#include <pcre2.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include "corpus.h"

static volatile int sink;
static uint64_t now(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return (uint64_t)t.tv_sec * 1000000000 + t.tv_nsec;
}
static void hex(const char *s) {
    putchar('"');
    for (; *s; s++) printf("%02x", (unsigned char)*s);
    putchar('"');
}
static void offsets(pcre2_match_data *md, int rc, uint32_t count) {
    if (rc < 0) { printf("null"); return; }
    PCRE2_SIZE *v = pcre2_get_ovector_pointer(md);
    putchar('[');
    for (uint32_t i = 0; i < 2 * (count + 1); i++) {
        if (i) putchar(',');
        /* Captures above rc are unset, regardless of previous match data. */
        printf("%lld", i >= 2 * (uint32_t)rc || v[i] == PCRE2_UNSET ? -1LL : (long long)v[i]);
    }
    putchar(']');
}
struct flag_map { uint32_t native; int go_bit; };
static const struct flag_map flags[] = {
    {PCRE2_CASELESS,0}, {PCRE2_MULTILINE,1}, {PCRE2_DOTALL,2},
    {PCRE2_EXTENDED,3}, {PCRE2_EXTENDED_MORE,4}, {PCRE2_ANCHORED,5},
    {PCRE2_ENDANCHORED,6}, {PCRE2_DOLLAR_ENDONLY,7}, {PCRE2_UNGREEDY,8},
    {PCRE2_UTF,9}, {PCRE2_UCP,10}, {PCRE2_DUPNAMES,11},
    {PCRE2_NO_AUTO_CAPTURE,12}, {PCRE2_FIRSTLINE,13}, {PCRE2_LITERAL,14},
    {PCRE2_NO_AUTO_POSSESS,24}, {PCRE2_NO_DOTSTAR_ANCHOR,25},
    {PCRE2_NO_START_OPTIMIZE,26}, {PCRE2_MATCH_UNSET_BACKREF,27}
};
static double measure(pcre2_code *re, struct regression_test_case *t,
                      pcre2_match_data *md, pcre2_match_context *mc,
                      int mode, uint64_t target) {
    size_t len = strlen(t->input);
    uint64_t n = 1, elapsed;
    do {
        uint64_t start = now();
        for (uint64_t i = 0; i < n; i++) {
            int rc;
            if (mode == 0) rc = pcre2_match(re, (PCRE2_SPTR)t->input, len,
                t->start_offset & OFFSET_MASK, t->match_options | PCRE2_NO_JIT, md, mc);
            else if (mode == 1) rc = pcre2_jit_match(re, (PCRE2_SPTR)t->input, len,
                t->start_offset & OFFSET_MASK, t->match_options, md, mc);
            else rc = pcre2_match(re, (PCRE2_SPTR)t->input, len,
                t->start_offset & OFFSET_MASK, t->match_options, md, mc);
            sink = rc;
        }
        elapsed = now() - start;
        if (elapsed >= target) break;
        n *= 2;
    } while (n < (1ULL << 32));
    return (double)elapsed / n;
}
int main(int argc, char **argv) {
    int selected = argc > 1 ? atoi(argv[1]) : 0;
    int samples = argc > 2 ? atoi(argv[2]) : 0;
    uint64_t target = argc > 3 ? strtoull(argv[3], NULL, 10) : 3000000;
    int jit = 0;
    pcre2_config(PCRE2_CONFIG_JIT, &jit);
    if (!jit) return 2;
    for (int i = 0; regression_test_cases[i].pattern; i++) {
        if (selected && selected != i + 1) continue;
        struct regression_test_case *t = &regression_test_cases[i];
        uint32_t remaining = t->compile_options;
        uint64_t go_flags = 0;
        for (size_t j = 0; j < sizeof(flags)/sizeof(flags[0]); j++) {
            if (remaining & flags[j].native) {
                go_flags |= 1ULL << flags[j].go_bit;
                remaining &= ~flags[j].native;
            }
        }
        int nl = GET_NEWLINE(t->newline), bsr = GET_BSR(t->newline);
        if (nl) go_flags |= 1ULL << (14 + nl);
        if (bsr) go_flags |= 1ULL << (bsr == PCRE2_BSR_ANYCRLF ? 22 : 23);
        const char *skip = NULL;
        if (t->start_offset & F_NO8) skip = "not_8bit";
        else if (t->start_offset & F_DIFF) skip = "upstream_no_comparison";
        else if (remaining) skip = "unsupported_compile_options";
        else if (t->match_options) skip = "unsupported_match_options";
        else if (t->start_offset & OFFSET_MASK) skip = "start_offset_api";
        printf("{\"id\":%d,\"pattern_hex\":", i + 1); hex(t->pattern);
        printf(",\"subject_hex\":"); hex(t->input);
        printf(",\"compile_options\":%u,\"newline\":%d,\"match_options\":%d,\"start_offset\":%d,\"go_flags\":%llu",
            t->compile_options, t->newline, t->match_options, t->start_offset,
            (unsigned long long)go_flags);
        if (skip) { printf(",\"skip\":\"%s\"}\n", skip); continue; }
        pcre2_compile_context *cc = pcre2_compile_context_create(NULL);
        if (nl) pcre2_set_newline(cc, nl);
        if (bsr) pcre2_set_bsr(cc, bsr);
        int err;
        PCRE2_SIZE off;
        uint64_t start = now();
        pcre2_code *re = pcre2_compile((PCRE2_SPTR)t->pattern, PCRE2_ZERO_TERMINATED,
            t->compile_options, &err, &off, cc);
        uint64_t compile_ns = now() - start;
        pcre2_compile_context_free(cc);
        if (!re) { printf(",\"skip\":\"native_compile_error\",\"error\":%d}\n", err); continue; }
        uint32_t count;
        pcre2_pattern_info(re, PCRE2_INFO_CAPTURECOUNT, &count);
        pcre2_match_data *md = pcre2_match_data_create_from_pattern(re, NULL);
        pcre2_match_context *mc = pcre2_match_context_create(NULL);
        pcre2_set_match_limit(mc, 10000000);
        pcre2_jit_stack *stack = pcre2_jit_stack_create(32768, 1024*1024, NULL);
        pcre2_jit_stack_assign(mc, NULL, stack);
        int rc = pcre2_match(re, (PCRE2_SPTR)t->input, strlen(t->input), 0,
            PCRE2_NO_JIT, md, mc);
        printf(",\"compile_ns\":%llu,\"rc\":%d,\"captures\":", (unsigned long long)compile_ns, rc);
        offsets(md, rc, count);
        start = now();
        int jerr = pcre2_jit_compile(re, PCRE2_JIT_COMPLETE);
        printf(",\"jit_compile_ns\":%llu,\"jit_error\":%d", (unsigned long long)(now()-start), jerr);
        size_t code_bytes = 0;
        pcre2_pattern_info(re, PCRE2_INFO_JITSIZE, &code_bytes);
        printf(",\"code_bytes\":%zu", code_bytes);
        if (!jerr) {
            int jrc = pcre2_jit_match(re, (PCRE2_SPTR)t->input, strlen(t->input), 0, 0, md, mc);
            printf(",\"jit_rc\":%d,\"jit_captures\":", jrc);
            offsets(md, jrc, count);
        }
        if (samples && !jerr && rc >= -1) {
            double *times = calloc(3 * samples, sizeof(double));
            /* Rotate modes to reduce systematic ordering bias. */
            for (int s = 0; s < samples; s++)
                for (int k = 0; k < 3; k++) {
                    int mode = (s + k) % 3;
                    times[mode * samples + s] = measure(re, t, md, mc, mode, target);
                }
            const char *names[] = {"interpreter_ns", "jit_ns", "jit_api_ns"};
            for (int mode = 0; mode < 3; mode++) {
                printf(",\"%s\":[", names[mode]);
                for (int s = 0; s < samples; s++) printf("%s%.3f", s ? "," : "", times[mode*samples+s]);
                putchar(']');
            }
            free(times);
        }
        printf("}\n"); fflush(stdout);
        pcre2_jit_stack_free(stack);
        pcre2_match_context_free(mc);
        pcre2_match_data_free(md);
        pcre2_code_free(re);
    }
    return 0;
}
