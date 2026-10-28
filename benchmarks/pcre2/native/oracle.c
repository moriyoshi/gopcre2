/* Small binary-safe correctness oracle. Build against the pinned PCRE2 source. */
#define PCRE2_CODE_UNIT_WIDTH 8
#include <pcre2.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static unsigned char *unhex(const char *s, size_t *length) {
    *length = strlen(s) / 2;
    unsigned char *out = malloc(*length + 1);
    for (size_t i = 0; i < *length; i++) {
        unsigned int byte;
        if (sscanf(s + 2*i, "%2x", &byte) != 1) exit(2);
        out[i] = byte;
    }
    out[*length] = 0;
    return out;
}

int main(int argc, char **argv) {
    if (argc != 4) return 2;
    size_t plen, slen;
    unsigned char *pattern = unhex(argv[1], &plen), *subject = unhex(argv[2], &slen);
    unsigned long long flags = strtoull(argv[3], NULL, 10);
    uint32_t options = 0;
    if (flags & (1ULL << 0)) options |= PCRE2_CASELESS;
    if (flags & (1ULL << 1)) options |= PCRE2_MULTILINE;
    if (flags & (1ULL << 2)) options |= PCRE2_DOTALL;
    if (flags & (1ULL << 5)) options |= PCRE2_ANCHORED;
    if (flags & (1ULL << 9)) options |= PCRE2_UTF;
    if (flags & (1ULL << 10)) options |= PCRE2_UCP;
    pcre2_compile_context *cc = pcre2_compile_context_create(NULL);
    for (int bit = 15; bit <= 20; bit++)
        if (flags & (1ULL << bit)) pcre2_set_newline(cc, bit - 14);
    int error;
    PCRE2_SIZE offset;
    pcre2_code *code = pcre2_compile(pattern, plen, options, &error, &offset, cc);
    if (!code) {
        printf("{\"compile_error\":%d,\"offset\":%zu}\n", error, (size_t)offset);
    } else {
        pcre2_match_data *md = pcre2_match_data_create_from_pattern(code, NULL);
        int rc = pcre2_match(code, subject, slen, 0, PCRE2_NO_JIT, md, NULL);
        printf("{\"rc\":%d,\"captures\":", rc);
        if (rc < 0) printf("null");
        else {
            uint32_t count;
            pcre2_pattern_info(code, PCRE2_INFO_CAPTURECOUNT, &count);
            PCRE2_SIZE *v = pcre2_get_ovector_pointer(md);
            putchar('[');
            for (uint32_t i = 0; i < 2*(count + 1); i++)
                printf("%s%lld", i ? "," : "", i >= 2*(uint32_t)rc || v[i] == PCRE2_UNSET ? -1LL : (long long)v[i]);
            putchar(']');
        }
        /* General API validates UTF before entering JIT code. Direct JIT on
           deliberately invalid UTF would violate its input contract. */
        uint32_t count;
        pcre2_pattern_info(code, PCRE2_INFO_CAPTURECOUNT, &count);
        size_t bytes = 2*(count + 1)*sizeof(PCRE2_SIZE);
        PCRE2_SIZE *saved = malloc(bytes);
        memcpy(saved, pcre2_get_ovector_pointer(md), bytes);
        if (pcre2_jit_compile(code, PCRE2_JIT_COMPLETE) != 0) return 3;
        int jrc = pcre2_match(code, subject, slen, 0, 0, md, NULL);
        if (jrc != rc || (rc >= 0 && memcmp(saved,
            pcre2_get_ovector_pointer(md), 2*(size_t)rc*sizeof(PCRE2_SIZE)) != 0)) return 3;
        printf(",\"jit_rc\":%d", jrc);
        free(saved);
        puts("}");
        pcre2_match_data_free(md);
        pcre2_code_free(code);
    }
    pcre2_compile_context_free(cc);
    free(pattern);
    free(subject);
    return 0;
}
