package mod

import (
	"fmt"
	"strings"
)

// Position describes a source position in a vs.mod file.
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
	p.LineRune += len([]rune(s))
	return p
}

// SyntaxError reports a vs.mod parse error at a specific position.
// This is the error type Parse and ParseLax return for malformed
// input, formatted as file:line:col.
type SyntaxError struct {
	Filename string
	Pos      Position
	Err      error
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s:%d:%d: %v", e.Filename, e.Pos.Line, e.Pos.LineRune, e.Err)
}

func (e *SyntaxError) Unwrap() error { return e.Err }

// Comment is a single "//" comment.
type Comment struct {
	Start  Position
	Token  string // comment text, without the trailing newline
	Suffix bool   // true for an end-of-line comment; false for a whole-line comment
}

// Comments collects the comments attached to one syntax node.
type Comments struct {
	Before []Comment // whole-line comments immediately preceding the node
	Suffix []Comment // end-of-line comment(s) following the node on the same line
}

func (c *Comments) Comment() *Comments { return c }

// Expr is satisfied by every node that can appear in a vs.mod
// syntax tree: CommentBlock, *Line, and *LineBlock.
type Expr interface {
	Comment() *Comments
}

// Line is one directive spec: a bare top-level statement
// ("vertex 1.0.0"), or one entry inside a factored block
// ("github.com/x/y v1.0.0" inside "dependencies ( ... )").
type Line struct {
	Comments
	Start   Position
	Token   []string
	InBlock bool
	End     Position
}

func (x *Line) Comment() *Comments          { return &x.Comments }
func (x *Line) Span() (start, end Position) { return x.Start, x.End }

// LineBlock is a factored block: a keyword, "(", a sequence of
// Lines, and a closing ")" — e.g. "dependencies ( ... )".
type LineBlock struct {
	Comments
	Start  Position
	LParen LParen
	Token  []string // the block keyword, e.g. []string{"dependencies"}
	Line   []*Line
	RParen RParen
}

func (x *LineBlock) Comment() *Comments          { return &x.Comments }
func (x *LineBlock) Span() (start, end Position) { return x.Start, x.RParen.Pos }

// LParen and RParen are a LineBlock's delimiters. Each carries its
// own position and any comments attached directly to that paren.
type LParen struct {
	Comments
	Pos Position
}

func (x *LParen) Comment() *Comments { return &x.Comments }

type RParen struct {
	Comments
	Pos Position
}

func (x *RParen) Comment() *Comments { return &x.Comments }

// CommentBlock is a run of comment lines with no attached directive
// — a file header, or a blank-line-separated "Deprecated:" note.
type CommentBlock struct {
	Comments
	Start Position
}

func (x *CommentBlock) Comment() *Comments { return &x.Comments }

// FileSyntax is the uninterpreted syntax tree of a vs.mod file: every
// directive, comment, and blank-line-separated comment block, in
// source order, with full position information. File (file.go) is
// built by walking this tree and interpreting each node by keyword.
type FileSyntax struct {
	Name string // filename, for error messages
	Stmt []Expr
}