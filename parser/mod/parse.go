package mod

import (
	"fmt"
)

// Parse parses a vs.mod file. filename is used only for error
// messages. fix, if non-nil, canonicalizes versions as they're
// encountered (e.g. resolving a branch name to a pseudo-version).
//
// Unrecognized directives are a hard error — use ParseLax for a
// vs.mod that may have been written against a newer compiler.
func Parse(filename string, data []byte, fix VersionFixer) (f *File, err error) {
	return parse(filename, data, fix, false)
}

// ParseLax is Parse but ignores directives it doesn't recognize,
// tolerating a file written against a newer toolchain's vocabulary.
func ParseLax(filename string, data []byte, fix VersionFixer) (f *File, err error) {
	return parse(filename, data, fix, true)
}

func parse(filename string, data []byte, fix VersionFixer, lax bool) (fl *File, err error) {
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(*SyntaxError); ok {
				err = se
				return
			}
			panic(r)
		}
	}()

	fs := readFile(filename, data)
	f := &File{Syntax: fs}

	for _, x := range fs.Stmt {
		switch x := x.(type) {
		case *Line:
			if err := f.interpret(filename, x.Token[0], x.Token[1:], x, fix, lax); err != nil {
				return nil, err
			}
		case *LineBlock:
			for _, l := range x.Line {
				if err := f.interpret(filename, x.Token[0], l.Token, l, fix, lax); err != nil {
					return nil, err
				}
			}
		}
	}

	if f.Module == nil {
		return nil, &SyntaxError{Filename: filename, Err: fmt.Errorf("missing module directive")}
	}
	return f, nil
}

// readFile tokenizes data into a FileSyntax: a sequence of bare
// Lines and factored LineBlocks in source order, per vs.mod's
// File = { Directive } top-level production.
func readFile(filename string, data []byte) *FileSyntax {
	in := newInput(filename, data)
	fs := &FileSyntax{Name: filename}

	for {
		tok := in.peek()
		if tok.kind == tokEOF {
			break
		}
		if tok.kind == tokNewline {
			in.lex() // blank line; comments (if any) stay pending for the next stmt
			continue
		}
		fs.Stmt = append(fs.Stmt, in.parseStmt())
	}
	return fs
}

// parseStmt reads one top-level directive: either a bare Line
// ("vertex 1.0.0") or, if the keyword is followed by "(", a
// LineBlock ("dependencies ( ... )").
func (in *input) parseStmt() Expr {
	before := in.comments
	in.comments = nil

	kw := in.lex()
	if kw.kind != tokIdent && kw.kind != tokString {
		panic(in.errorf(kw.pos, "unexpected token at start of line"))
	}

	if in.peek().kind == tokLParen {
		return in.parseLineBlock(kw, before)
	}
	return in.parseLine(kw, before)
}

func (in *input) parseLine(kw token, before []Comment) *Line {
	l := &Line{Start: kw.pos, Token: []string{kw.text}}
	l.Comments.Before = before
	for {
		tok := in.peek()
		if tok.kind.isEOL() {
			l.End = tok.pos
			if tok.kind == tokNewline {
				in.lex()
			}
			l.Comments.Suffix = trailingSuffixComments(&in.comments, tok.pos)
			return l
		}
		in.lex()
		l.Token = append(l.Token, tok.text)
		l.End = tok.endPos
	}
}

func (in *input) parseLineBlock(kw token, before []Comment) *LineBlock {
	x := &LineBlock{Start: kw.pos, Token: []string{kw.text}}
	x.Comments.Before = before

	lp := in.lex() // "("
	x.LParen.Pos = lp.pos
	if nl := in.lex(); nl.kind != tokNewline {
		panic(in.errorf(nl.pos, "expected newline after '('"))
	}

	for {
		tok := in.peek()
		if tok.kind == tokRParen {
			rp := in.lex()
			x.RParen.Pos = rp.pos
			x.RParen.Comments.Before = in.comments
			in.comments = nil
			if nl := in.lex(); !nl.kind.isEOL() {
				panic(in.errorf(nl.pos, "expected newline after ')'"))
			}
			return x
		}
		if tok.kind == tokNewline {
			in.lex()
			continue
		}
		before := in.comments
		in.comments = nil
		lineStart := in.lex()
		l := &Line{Start: lineStart.pos, Token: []string{lineStart.text}, InBlock: true}
		l.Comments.Before = before
		for {
			t := in.peek()
			if t.kind.isEOL() {
				l.End = t.pos
				if t.kind == tokNewline {
					in.lex()
				}
				l.Comments.Suffix = trailingSuffixComments(&in.comments, t.pos)
				break
			}
			in.lex()
			l.Token = append(l.Token, t.text)
			l.End = t.endPos
		}
		x.Line = append(x.Line, l)
	}
}

// trailingSuffixComments pulls any comments collected since the
// last real token off in.comments and returns them as this node's
// Suffix, provided they were marked Suffix (i.e. something preceded
// them on the same physical line).
func trailingSuffixComments(pending *[]Comment, before Position) []Comment {
	var suffix, rest []Comment
	for _, c := range *pending {
		if c.Suffix {
			suffix = append(suffix, c)
		} else {
			rest = append(rest, c)
		}
	}
	*pending = rest
	return suffix
}

// interpret dispatches one directive spec (a bare Line's keyword +
// args, or one entry inside a LineBlock) to its per-directive parser.
func (f *File) interpret(filename, keyword string, args []string, line *Line, fix VersionFixer, lax bool) error {
	wrap := func(err error) error {
		return &SyntaxError{Filename: filename, Pos: line.Start, Err: err}
	}

	switch keyword {
	case "module":
		if len(args) != 1 {
			return wrap(fmt.Errorf("usage: module module/path"))
		}
		mod := &Module{Path: ModulePath(args[0]), Syntax: line}
		mod.Deprecated = deprecationMessage(line.Comments)
		f.Module = mod

	case "vertex":
		if len(args) != 1 {
			return wrap(fmt.Errorf("usage: vertex 1.2.3"))
		}
		if !IsValidVertexVersion(args[0]) {
			return wrap(fmt.Errorf("invalid vertex version %q", args[0]))
		}
		f.Vertex = &Vertex{Version: args[0], Syntax: line}

	case "toolchain":
		if len(args) != 1 {
			return wrap(fmt.Errorf("usage: toolchain name"))
		}
		f.Toolchain = &Toolchain{Name: args[0], Syntax: line}

	case "dependencies":
		mv, indirect, err := parseModuleVersionSpec(args, line, fix)
		if err != nil {
			return wrap(err)
		}
		f.Dependencies = append(f.Dependencies, &Dependency{Mod: mv, Indirect: indirect, Syntax: line})

	case "exclude":
		mv, _, err := parseModuleVersionSpec(args, line, fix)
		if err != nil {
			return wrap(err)
		}
		f.Exclude = append(f.Exclude, &Exclude{Mod: mv, Syntax: line})

	case "replace":
		r, err := parseReplaceSpec(args, fix)
		if err != nil {
			return wrap(err)
		}
		r.Syntax = line
		f.Replace = append(f.Replace, r)

	case "retract":
		r, err := parseRetractSpec(args)
		if err != nil {
			return wrap(err)
		}
		r.Rationale = rationaleComment(line.Comments)
		r.Syntax = line
		f.Retract = append(f.Retract, r)

	case "tool":
		if len(args) != 1 {
			return wrap(fmt.Errorf("usage: tool module/path"))
		}
		f.Tool = append(f.Tool, &Tool{Path: ModulePath(args[0]), Syntax: line})

	case "ignore":
		if len(args) != 1 {
			return wrap(fmt.Errorf("usage: ignore path"))
		}
		f.Ignore = append(f.Ignore, &Ignore{Path: args[0], Syntax: line})

	default:
		if lax {
			return nil
		}
		return wrap(fmt.Errorf("unknown directive %q", keyword))
	}
	return nil
}

// parseModuleVersionSpec handles DependencySpec / ExcludeSpec:
// "ModulePath Version", with an optional trailing "// indirect"
// suffix comment recognized for dependencies.
func parseModuleVersionSpec(args []string, line *Line, fix VersionFixer) (ModuleVersion, bool, error) {
	if len(args) != 2 {
		return ModuleVersion{}, false, fmt.Errorf("usage: module/path v1.2.3")
	}
	path, vers := args[0], args[1]
	if fix != nil {
		fixed, err := fix(path, vers)
		if err != nil {
			return ModuleVersion{}, false, err
		}
		vers = fixed
	}
	indirect := false
	for _, c := range line.Comments.Suffix {
		if c.Token == "indirect" {
			indirect = true
		}
	}
	return ModuleVersion{Path: ModulePath(path), Version: vers}, indirect, nil
}

// parseReplaceSpec handles:
//
//	ModulePath [ Version ] "=>" FilePath
//	ModulePath [ Version ] "=>" ModulePath Version
func parseReplaceSpec(args []string, fix VersionFixer) (*Replace, error) {
	arrow := -1
	for i, a := range args {
		if a == "=>" {
			arrow = i
			break
		}
	}
	if arrow < 0 {
		return nil, fmt.Errorf("usage: old/path [v1.2.3] => new/path [v1.2.3]")
	}
	old, err := parseOneOrTwo(args[:arrow])
	if err != nil {
		return nil, err
	}
	new, err := parseOneOrTwo(args[arrow+1:])
	if err != nil {
		return nil, err
	}
	if fix != nil {
		if old.Version != "" {
			v, err := fix(old.Path, old.Version)
			if err != nil {
				return nil, err
			}
			old.Version = v
		}
		if new.Version != "" && !isFilePath(new.Path) {
			v, err := fix(new.Path, new.Version)
			if err != nil {
				return nil, err
			}
			new.Version = v
		}
	}
	return &Replace{Old: old, New: new}, nil
}

func parseOneOrTwo(args []string) (ModuleVersion, error) {
	switch len(args) {
	case 1:
		return ModuleVersion{Path: ModulePath(args[0])}, nil
	case 2:
		return ModuleVersion{Path: ModulePath(args[0]), Version: args[1]}, nil
	default:
		return ModuleVersion{}, fmt.Errorf("expected 'path' or 'path version'")
	}
}

func isFilePath(p ModulePath) bool {
	s := string(p)
	return len(s) > 0 && (s[0] == '.' || s[0] == '/')
}

// parseRetractSpec handles:
//
//	Version
//	"[" Version "," Version "]"
func parseRetractSpec(args []string) (*Retract, error) {
	if len(args) == 1 {
		return &Retract{VersionInterval: VersionInterval{Low: args[0], High: args[0]}}, nil
	}
	if len(args) == 5 && args[0] == "[" && args[2] == "," && args[4] == "]" {
		return &Retract{VersionInterval: VersionInterval{Low: args[1], High: args[3]}}, nil
	}
	return nil, fmt.Errorf("usage: retract v1.2.3 OR retract [v1.2.3, v1.4.5]")
}

func deprecationMessage(c Comments) string {
	return findRationale(c, "Deprecated:")
}

func rationaleComment(c Comments) string {
	if len(c.Suffix) > 0 {
		return c.Suffix[0].Token
	}
	if len(c.Before) > 0 {
		return c.Before[len(c.Before)-1].Token
	}
	return ""
}

func findRationale(c Comments, prefix string) string {
	all := append(append([]Comment{}, c.Before...), c.Suffix...)
	for _, cm := range all {
		if len(cm.Token) >= len(prefix) && cm.Token[:len(prefix)] == prefix {
			return trimSpace(cm.Token[len(prefix):])
		}
	}
	return ""
}

func trimSpace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	return s
}