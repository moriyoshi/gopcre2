package gopcre2

import (
	"unicode"
	"unicode/utf8"
)

// PCRE2-8 treats every byte as a character unless UTF is explicitly enabled.
// UCP changes character properties, independently of the encoding.
func decodeCharacter(s string, flags Flag) (rune, int) {
	if flags&UTF == 0 && len(s) != 0 {
		return rune(s[0]), 1
	}
	return utf8.DecodeRuneInString(s)
}

func decodeLastCharacter(s string, flags Flag) (rune, int) {
	if flags&UTF == 0 && len(s) != 0 {
		return rune(s[len(s)-1]), 1
	}
	return utf8.DecodeLastRuneInString(s)
}

func foldCharacter(r rune, flags Flag) rune {
	if flags&(UTF|UCP) != 0 {
		return unicode.SimpleFold(r)
	}
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	if r >= 'a' && r <= 'z' {
		return r - ('a' - 'A')
	}
	return r
}
