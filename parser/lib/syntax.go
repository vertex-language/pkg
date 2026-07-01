package lib

// Comment is a single "//..." comment.
type Comment struct {
	Start Position
	Token string // text following "//"
}

// Comments holds the comments attached to a statement: whole lines
// immediately preceding it, and a same-line comment following it.
type Comments struct {
	Before []Comment
	Suffix []Comment
}

// Expr is a top-level statement in a vs.lib file: a *LibraryLine, a
// *FieldLine (a Meta line), or a *ProviderBlock.
type Expr interface {
	Span() (start, end Position)
	comments() *Comments
}

// FileSyntax is the uninterpreted syntax tree for a vs.lib file,
// analogous to mod.FileSyntax for vs.mod.
type FileSyntax struct {
	Name string // filename
	Stmt []Expr
}

// LibraryLine is the `library <import-path>` meta line — the vs.lib
// analogue of vs.mod's `module` line. Unlike FieldLine it is not an
// `ident "=" string` production: it takes a bare, unquoted import path
// (Path) rather than a quoted Value, which is why it's its own Expr
// rather than a FieldLine with Key "library".
type LibraryLine struct {
	Comments
	Start, End Position
	Path       string // bare import path, e.g. "github.com/username/sqlite3"
}

func (x *LibraryLine) Span() (Position, Position) { return x.Start, x.End }
func (x *LibraryLine) comments() *Comments         { return &x.Comments }

// FieldLine is an `ident = string` line — used both for top-level Meta
// lines (version/description) and for Fields inside a provider or
// target block.
type FieldLine struct {
	Comments
	Start, End Position
	Key        string
	Value      string
}

func (x *FieldLine) Span() (Position, Position) { return x.Start, x.End }
func (x *FieldLine) comments() *Comments         { return &x.Comments }

// TargetBlock is `target "tag" [release "r"] { ... }`.
type TargetBlock struct {
	Comments
	Start, End Position
	Tag        string
	Release    string // "" if absent
	Fields     []*FieldLine
}

// ProviderBlock is `provider Kind { ... }`.
type ProviderBlock struct {
	Comments
	Start, End Position
	Kind       string
	Fields     []*FieldLine
	Targets    []*TargetBlock
}

func (x *ProviderBlock) Span() (Position, Position) { return x.Start, x.End }
func (x *ProviderBlock) comments() *Comments         { return &x.Comments }