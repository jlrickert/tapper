package omegadsl

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokKind int

const (
	tEOF tokKind = iota
	tNumber
	tString
	tIdent  // bare word; the parser classifies keywords/namespaces by text
	tQIdent // backtick-quoted name, always a plain name (never a keyword)
	tLParen
	tRParen
	tLBrace
	tRBrace
	tComma
	tColon
	tDot
	tArrow  // =>
	tAssign // =
	tEq     // ==
	tNeq    // !=
	tLt
	tLte
	tGt
	tGte
	tPlus
	tMinus
	tStar
	tSlash
)

type token struct {
	kind tokKind
	text string
	num  float64
	line int
}

func (t token) String() string {
	switch t.kind {
	case tEOF:
		return "end of input"
	case tNumber:
		return fmt.Sprintf("number %v", t.num)
	case tString:
		return fmt.Sprintf("string %q", t.text)
	case tIdent, tQIdent:
		return fmt.Sprintf("%q", t.text)
	default:
		return fmt.Sprintf("%q", t.text)
	}
}

// lex tokenizes src. Whitespace, newlines, and # comments are insignificant and
// dropped; the language is layout-insensitive.
func lex(src string) ([]token, error) {
	var toks []token
	line := 1
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '(':
			toks = append(toks, token{kind: tLParen, text: "(", line: line})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen, text: ")", line: line})
			i++
		case c == '{':
			toks = append(toks, token{kind: tLBrace, text: "{", line: line})
			i++
		case c == '}':
			toks = append(toks, token{kind: tRBrace, text: "}", line: line})
			i++
		case c == ',':
			toks = append(toks, token{kind: tComma, text: ",", line: line})
			i++
		case c == ':':
			toks = append(toks, token{kind: tColon, text: ":", line: line})
			i++
		case c == '.':
			toks = append(toks, token{kind: tDot, text: ".", line: line})
			i++
		case c == '+':
			toks = append(toks, token{kind: tPlus, text: "+", line: line})
			i++
		case c == '-':
			toks = append(toks, token{kind: tMinus, text: "-", line: line})
			i++
		case c == '*':
			toks = append(toks, token{kind: tStar, text: "*", line: line})
			i++
		case c == '/':
			toks = append(toks, token{kind: tSlash, text: "/", line: line})
			i++
		case c == '=':
			if i+1 < len(src) && src[i+1] == '>' {
				toks = append(toks, token{kind: tArrow, text: "=>", line: line})
				i += 2
			} else if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{kind: tEq, text: "==", line: line})
				i += 2
			} else {
				toks = append(toks, token{kind: tAssign, text: "=", line: line})
				i++
			}
		case c == '!':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{kind: tNeq, text: "!=", line: line})
				i += 2
			} else {
				return nil, fmt.Errorf("line %d: unexpected %q (did you mean \"!=\" or \"not\"?)", line, "!")
			}
		case c == '<':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{kind: tLte, text: "<=", line: line})
				i += 2
			} else {
				toks = append(toks, token{kind: tLt, text: "<", line: line})
				i++
			}
		case c == '>':
			if i+1 < len(src) && src[i+1] == '=' {
				toks = append(toks, token{kind: tGte, text: ">=", line: line})
				i += 2
			} else {
				toks = append(toks, token{kind: tGt, text: ">", line: line})
				i++
			}
		case c == '"':
			text, n, err := lexString(src[i:], line)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tString, text: text, line: line})
			i += n
		case c == '`':
			text, n, err := lexQuotedName(src[i:], line)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tQIdent, text: text, line: line})
			i += n
		case c >= '0' && c <= '9':
			num, text, n, err := lexNumber(src[i:], line)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tNumber, text: text, num: num, line: line})
			i += n
		case isIdentStart(rune(c)):
			text, n := lexIdent(src[i:])
			toks = append(toks, token{kind: tIdent, text: text, line: line})
			i += n
		default:
			r, _ := utf8.DecodeRuneInString(src[i:])
			return nil, fmt.Errorf("line %d: unexpected character %q", line, r)
		}
	}
	toks = append(toks, token{kind: tEOF, text: "", line: line})
	return toks, nil
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentCont(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func lexIdent(s string) (string, int) {
	i := 0
	for i < len(s) {
		r, w := utf8.DecodeRuneInString(s[i:])
		if !isIdentCont(r) {
			break
		}
		i += w
	}
	return s[:i], i
}

func lexNumber(s string, line int) (float64, string, int, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return 0, "", 0, fmt.Errorf("line %d: malformed number %q", line, s[:i])
		}
	}
	// Reject numbers glued to a letter (e.g. 90d): duration literals are not yet
	// supported, and a bare "90d" is otherwise a confusing two-token sequence.
	if i < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[i:]); isIdentStart(r) {
			return 0, "", 0, fmt.Errorf("line %d: unexpected %q after number %q (duration literals are not supported yet)", line, string(r), s[:i])
		}
	}
	text := s[:i]
	num, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, "", 0, fmt.Errorf("line %d: invalid number %q", line, text)
	}
	return num, text, i, nil
}

func lexString(s string, line int) (string, int, error) {
	var b strings.Builder
	i := 1 // skip opening quote
	for i < len(s) {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		if c == '"' {
			return b.String(), i + 1, nil
		}
		if c == '\n' {
			break
		}
		b.WriteByte(c)
		i++
	}
	return "", 0, fmt.Errorf("line %d: unterminated string literal", line)
}

func lexQuotedName(s string, line int) (string, int, error) {
	i := 1 // skip opening backtick
	for i < len(s) {
		if s[i] == '`' {
			name := s[1:i]
			if name == "" {
				return "", 0, fmt.Errorf("line %d: empty backtick-quoted name", line)
			}
			return name, i + 1, nil
		}
		if s[i] == '\n' {
			break
		}
		i++
	}
	return "", 0, fmt.Errorf("line %d: unterminated backtick-quoted name", line)
}
