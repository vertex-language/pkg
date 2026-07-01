package mod

import (
	"strings"
	"unicode/utf8"
)

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNewline
	tokIdent  // bare token: identifier, module path, version, "=>", "[", "]", ","
	tokString // quoted or raw string; text is the *unquoted* value
	tokLParen
	tokRParen
)

func (k tokenKind) isEOL() bool { return k == tokEOF || k == tokNewline }

type token struct {
	kind   tokenKind
	pos    Position
	endPos Position
	text   string
}

type input struct {
	filename string
	data     []byte
	pos      Position

	comments   []Comment // whole-file accumulator; flushed by the reader in parse.go
	sawTokLine bool       // has a non-comment token appeared yet on the current line?

	cur    token
	curSet bool
}

func newInput(filename string, data []byte) *input {
	return &input{filename: filename, data: data, pos: Position{Line: 1, LineRune: 1}}
}

func (in *input) errorf(pos Position, format string, args ...any) *SyntaxError {
	return &SyntaxError{Filename: in.filename, Pos: pos, Err: fmtErrorf(format, args...)}
}

// peek returns the next token without consuming it.
func (in *input) peek() token {
	if !in.curSet {
		in.cur = in.rawLex()
		in.curSet = true
	}
	return in.cur
}

// lex consumes and returns the next token.
func (in *input) lex() token {
	t := in.peek()
	in.curSet = false
	return t
}

func (in *input) advance(n int) {
	in.pos = in.pos.add(string(in.data[:n]))
	in.data = in.data[n:]
}

func (in *input) skipSpace() {
	for len(in.data) > 0 {
		switch in.data[0] {
		case ' ', '\t', '\r':
			in.advance(1)
		default:
			return
		}
	}
}

// rawLex reads the next non-comment token, silently collecting any
// "//" comments it passes over into in.comments — comments are
// invisible to the token stream proper.
func (in *input) rawLex() token {
	for {
		in.skipSpace()
		start := in.pos
		if len(in.data) == 0 {
			return token{kind: tokEOF, pos: start, endPos: start}
		}
		c := in.data[0]
		switch {
		case c == '\n':
			in.advance(1)
			in.sawTokLine = false
			return token{kind: tokNewline, pos: start, endPos: in.pos, text: "\n"}
		case c == '/' && len(in.data) > 1 && in.data[1] == '/':
			i := 2
			for i < len(in.data) && in.data[i] != '\n' {
				i++
			}
			text := string(in.data[2:i])
			in.comments = append(in.comments, Comment{
				Start:  start,
				Token:  strings.TrimSpace(text),
				Suffix: in.sawTokLine,
			})
			in.advance(i)
			continue // comments produce no token; loop for the next real one
		case c == '(':
			in.advance(1)
			in.sawTokLine = true
			return token{kind: tokLParen, pos: start, endPos: in.pos, text: "("}
		case c == ')':
			in.advance(1)
			in.sawTokLine = true
			return token{kind: tokRParen, pos: start, endPos: in.pos, text: ")"}
		case c == '"':
			in.sawTokLine = true
			return in.lexQuoted(start)
		case c == '`':
			in.sawTokLine = true
			return in.lexRaw(start)
		case c == '[' || c == ']' || c == ',':
			in.advance(1)
			in.sawTokLine = true
			return token{kind: tokIdent, pos: start, endPos: in.pos, text: string(c)}
		case c == '=' && len(in.data) > 1 && in.data[1] == '>':
			in.advance(2)
			in.sawTokLine = true
			return token{kind: tokIdent, pos: start, endPos: in.pos, text: "=>"}
		default:
			in.sawTokLine = true
			return in.lexIdent(start)
		}
	}
}

func (in *input) lexQuoted(start Position) token {
	var b strings.Builder
	in.advance(1) // opening quote
	for {
		if len(in.data) == 0 || in.data[0] == '\n' {
			panic(in.errorf(start, "unterminated string"))
		}
		switch {
		case in.data[0] == '"':
			in.advance(1)
			return token{kind: tokString, pos: start, endPos: in.pos, text: b.String()}
		case in.data[0] == '\\' && len(in.data) > 1:
			b.WriteByte(in.data[1])
			in.advance(2)
		default:
			r, size := utf8.DecodeRune(in.data)
			b.WriteRune(r)
			in.advance(size)
		}
	}
}

func (in *input) lexRaw(start Position) token {
	in.advance(1) // opening backtick
	i := 0
	for i < len(in.data) && in.data[i] != '`' {
		i++
	}
	if i == len(in.data) {
		panic(in.errorf(start, "unterminated raw string"))
	}
	text := string(in.data[:i])
	in.advance(i + 1)
	return token{kind: tokString, pos: start, endPos: in.pos, text: text}
}

// lexIdent consumes a run of non-whitespace, non-punctuation bytes:
// module paths, versions, directive keywords, everything that isn't
// quoted, a paren, or one of the special single/double-char tokens
// handled above.
func (in *input) lexIdent(start Position) token {
	i := 0
	for i < len(in.data) {
		switch in.data[i] {
		case ' ', '\t', '\r', '\n', '(', ')', '"', '`', '[', ']', ',':
			goto done
		}
		i++
	}
done:
	if i == 0 {
		panic(in.errorf(start, "unexpected character %q", rune(in.data[0])))
	}
	text := string(in.data[:i])
	in.advance(i)
	return token{kind: tokIdent, pos: start, endPos: in.pos, text: text}
}