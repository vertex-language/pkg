package lib

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Position is a location in a vs.lib source file.
type Position struct {
	Line     int // 1-based line number
	LineRune int // 1-based rune offset within the line
	Byte     int // 0-based byte offset within the file
}

func (p Position) add(s string) Position {
	p.Byte += len(s)
	if n := strings.Count(s, "\n"); n > 0 {
		p.Line += n
		s = s[strings.LastIndex(s, "\n")+1:]
		p.LineRune = 1
	}
	p.LineRune += utf8.RuneCountInString(s)
	return p
}

// SyntaxError is returned for any lexing, parsing, or interpretation
// failure encountered while reading a vs.lib file.
type SyntaxError struct {
	Filename string
	Pos      Position
	Err      error
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s:%d:%d: %v", e.Filename, e.Pos.Line, e.Pos.LineRune, e.Err)
}

func (e *SyntaxError) Unwrap() error { return e.Err }

func syntaxErr(filename string, pos Position, format string, args ...interface{}) error {
	return &SyntaxError{Filename: filename, Pos: pos, Err: fmt.Errorf(format, args...)}
}

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNewline
	tokIdent
	tokString
	tokEquals
	tokLBrace
	tokRBrace
	tokComment
)

type token struct {
	kind tokenKind
	text string // identifier text, decoded string value, or comment text (without "//")
	pos  Position
	end  Position
}

type lexer struct {
	filename string
	data     []byte
	pos      Position
	i        int
}

func newLexer(filename string, data []byte) *lexer {
	return &lexer{filename: filename, data: data, pos: Position{Line: 1, LineRune: 1, Byte: 0}}
}

func (lx *lexer) advance(n int) Position {
	s := string(lx.data[lx.i : lx.i+n])
	lx.pos = lx.pos.add(s)
	lx.i += n
	return lx.pos
}

func (lx *lexer) advanceTo(j int) Position { return lx.advance(j - lx.i) }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isIdentPart additionally admits '.', '-', and '/' as continuation
// characters (but never as a start character, via isIdentStart). This
// is what lets an unquoted import path — e.g.
// "github.com/vertex-language/llama" after the `library` keyword — lex
// as a single token, the same way vs.mod's `module` line does. No
// other identifier in the grammar (Kind, Field keys, "target",
// "release", "provider", "library") legitimately contains these
// characters, so this is a strict widening: it only ever changes
// behavior where a bare path actually appears.
func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '/'
}

func (lx *lexer) scanString() (string, error) {
	startPos := lx.pos
	lx.advance(1) // opening quote
	var sb strings.Builder
	for {
		if lx.i >= len(lx.data) {
			return "", syntaxErr(lx.filename, startPos, "unterminated string")
		}
		c := lx.data[lx.i]
		if c == '"' {
			lx.advance(1)
			return sb.String(), nil
		}
		if c == '\n' {
			return "", syntaxErr(lx.filename, startPos, "unterminated string")
		}
		if c == '\\' && lx.i+1 < len(lx.data) {
			switch next := lx.data[lx.i+1]; next {
			case '"', '\\':
				sb.WriteByte(next)
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			default:
				sb.WriteByte(next)
			}
			lx.advance(2)
			continue
		}
		sb.WriteByte(c)
		lx.advance(1)
	}
}

func (lx *lexer) lex() ([]token, error) {
	var toks []token
	for lx.i < len(lx.data) {
		c := lx.data[lx.i]
		switch {
		case c == '\n':
			start := lx.pos
			toks = append(toks, token{kind: tokNewline, pos: start, end: lx.advance(1)})
		case c == ' ' || c == '\t' || c == '\r':
			lx.advance(1)
		case c == '/' && lx.i+1 < len(lx.data) && lx.data[lx.i+1] == '/':
			start := lx.pos
			j := lx.i
			for j < len(lx.data) && lx.data[j] != '\n' {
				j++
			}
			text := strings.TrimPrefix(string(lx.data[lx.i:j]), "//")
			lx.advanceTo(j)
			toks = append(toks, token{kind: tokComment, text: text, pos: start, end: lx.pos})
		case c == '=':
			start := lx.pos
			toks = append(toks, token{kind: tokEquals, text: "=", pos: start, end: lx.advance(1)})
		case c == '{':
			start := lx.pos
			toks = append(toks, token{kind: tokLBrace, text: "{", pos: start, end: lx.advance(1)})
		case c == '}':
			start := lx.pos
			toks = append(toks, token{kind: tokRBrace, text: "}", pos: start, end: lx.advance(1)})
		case c == '"':
			start := lx.pos
			val, err := lx.scanString()
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{kind: tokString, text: val, pos: start, end: lx.pos})
		case isIdentStart(c):
			start := lx.pos
			j := lx.i
			for j < len(lx.data) && isIdentPart(lx.data[j]) {
				j++
			}
			text := string(lx.data[lx.i:j])
			lx.advanceTo(j)
			toks = append(toks, token{kind: tokIdent, text: text, pos: start, end: lx.pos})
		default:
			return nil, syntaxErr(lx.filename, lx.pos, "unexpected character %q", c)
		}
	}
	toks = append(toks, token{kind: tokEOF, pos: lx.pos, end: lx.pos})
	return toks, nil
}