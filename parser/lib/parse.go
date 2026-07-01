package lib

import "fmt"

// Parse parses a vs.lib file and returns its interpreted form. An
// unrecognized top-level Meta key or an unrecognized Field inside a
// provider/target block is a hard error.
func Parse(filename string, data []byte) (*File, error) {
	return parseAndInterpret(filename, data, false)
}

// ParseLax is like Parse but silently ignores unrecognized top-level
// Meta keys and unrecognized Fields inside provider/target blocks,
// useful when reading a vs.lib that may target a newer compiler.
func ParseLax(filename string, data []byte) (*File, error) {
	return parseAndInterpret(filename, data, true)
}

func parseAndInterpret(filename string, data []byte, lax bool) (*File, error) {
	toks, err := newLexer(filename, data).lex()
	if err != nil {
		return nil, err
	}
	fs, err := (&parser{filename: filename, tokens: toks}).parseFile()
	if err != nil {
		return nil, err
	}
	return interpret(filename, fs, lax)
}

type parser struct {
	filename string
	tokens   []token
	pos      int
}

func (p *parser) peek() token { return p.tokens[p.pos] }

func (p *parser) next() token {
	t := p.tokens[p.pos]
	if t.kind != tokEOF {
		p.pos++
	}
	return t
}

func (p *parser) errorf(pos Position, format string, args ...interface{}) error {
	return syntaxErr(p.filename, pos, format, args...)
}

// stmtKey returns a label for a statement, used only in error
// messages (e.g. the meta-before-providers ordering check).
func stmtKey(stmt Expr) string {
	switch x := stmt.(type) {
	case *LibraryLine:
		return "library"
	case *FieldLine:
		return x.Key
	default:
		return "?"
	}
}

// parseFile implements `VsLib = { Meta } { Provider } .`
func (p *parser) parseFile() (*FileSyntax, error) {
	fs := &FileSyntax{Name: p.filename}
	var pendingBefore []Comment
	sawProvider := false

	for {
		for {
			t := p.peek()
			if t.kind == tokNewline {
				p.next()
				continue
			}
			if t.kind == tokComment {
				pendingBefore = append(pendingBefore, Comment{Start: t.pos, Token: t.text})
				p.next()
				if p.peek().kind == tokNewline {
					p.next()
				}
				continue
			}
			break
		}
		if p.peek().kind == tokEOF {
			break
		}

		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		stmt.comments().Before = pendingBefore
		pendingBefore = nil

		if pb, ok := stmt.(*ProviderBlock); ok {
			sawProvider = true
			_ = pb
		} else if sawProvider {
			start, _ := stmt.Span()
			return nil, p.errorf(start, "meta directive %q must appear before all provider blocks", stmtKey(stmt))
		}

		if p.peek().kind == tokComment {
			c := p.next()
			stmt.comments().Suffix = []Comment{{Start: c.pos, Token: c.text}}
		}
		if p.peek().kind == tokNewline {
			p.next()
		} else if p.peek().kind != tokEOF {
			return nil, p.errorf(p.peek().pos, "expected newline, found %q", p.peek().text)
		}

		fs.Stmt = append(fs.Stmt, stmt)
	}
	return fs, nil
}

func (p *parser) parseStmt() (Expr, error) {
	t := p.peek()
	if t.kind != tokIdent {
		return nil, p.errorf(t.pos, "expected identifier, found %q", t.text)
	}
	switch t.text {
	case "provider":
		return p.parseProvider()
	case "library":
		return p.parseLibrary()
	default:
		return p.parseField()
	}
}

// parseLibrary implements `Library = "library" ImportPath newline .`
// Unlike parseField, there's no "=" and no quoted string: ImportPath
// lexes as a single bare identifier token (isIdentPart in lex.go
// admits '.', '-', and '/' as continuation characters for exactly
// this purpose).
func (p *parser) parseLibrary() (*LibraryLine, error) {
	kw := p.next() // "library"
	pathTok := p.next()
	if pathTok.kind != tokIdent {
		return nil, p.errorf(pathTok.pos, "expected import path after 'library', found %q", pathTok.text)
	}
	return &LibraryLine{Start: kw.pos, End: pathTok.end, Path: pathTok.text}, nil
}

// parseField implements `Field = ident "=" string newline .` (also used
// for top-level `Meta` fields like version/description, which are the
// same production).
func (p *parser) parseField() (*FieldLine, error) {
	key := p.next()
	eq := p.next()
	if eq.kind != tokEquals {
		return nil, p.errorf(eq.pos, "expected '=' after %q", key.text)
	}
	val := p.next()
	if val.kind != tokString {
		return nil, p.errorf(val.pos, "expected string value for %q", key.text)
	}
	return &FieldLine{Start: key.pos, End: val.end, Key: key.text, Value: val.text}, nil
}

func (p *parser) parseBlockOpen(what, name string) error {
	lb := p.next()
	if lb.kind != tokLBrace {
		return p.errorf(lb.pos, "expected '{' in %s %s", what, name)
	}
	if p.peek().kind != tokNewline {
		return p.errorf(p.peek().pos, "expected newline after '{'")
	}
	p.next()
	return nil
}

// parseProvider implements:
//
//	Provider = "provider" Kind "{" newline { Field } { Target } "}" newline .
func (p *parser) parseProvider() (*ProviderBlock, error) {
	kw := p.next() // "provider"
	kindTok := p.next()
	if kindTok.kind != tokIdent {
		return nil, p.errorf(kindTok.pos, "expected provider kind, found %q", kindTok.text)
	}
	if err := p.parseBlockOpen("provider", kindTok.text); err != nil {
		return nil, err
	}

	pb := &ProviderBlock{Start: kw.pos, Kind: kindTok.text}
	var pendingBefore []Comment

	for {
		for {
			t := p.peek()
			if t.kind == tokNewline {
				p.next()
				continue
			}
			if t.kind == tokComment {
				pendingBefore = append(pendingBefore, Comment{Start: t.pos, Token: t.text})
				p.next()
				if p.peek().kind == tokNewline {
					p.next()
				}
				continue
			}
			break
		}
		if p.peek().kind == tokRBrace {
			break
		}
		if p.peek().kind == tokEOF {
			return nil, p.errorf(p.peek().pos, "unexpected EOF inside provider %s block", kindTok.text)
		}

		if p.peek().kind == tokIdent && p.peek().text == "target" {
			tb, err := p.parseTarget()
			if err != nil {
				return nil, err
			}
			tb.Before = pendingBefore
			pendingBefore = nil
			if p.peek().kind == tokComment {
				c := p.next()
				tb.Suffix = []Comment{{Start: c.pos, Token: c.text}}
			}
			if p.peek().kind == tokNewline {
				p.next()
			}
			pb.Targets = append(pb.Targets, tb)
			continue
		}

		if len(pb.Targets) > 0 {
			return nil, p.errorf(p.peek().pos, "fields must appear before targets in a provider block")
		}
		f, err := p.parseField()
		if err != nil {
			return nil, err
		}
		f.Before = pendingBefore
		pendingBefore = nil
		if p.peek().kind == tokComment {
			c := p.next()
			f.Suffix = []Comment{{Start: c.pos, Token: c.text}}
		}
		if p.peek().kind == tokNewline {
			p.next()
		} else if p.peek().kind != tokRBrace {
			return nil, p.errorf(p.peek().pos, "expected newline after field %q", f.Key)
		}
		pb.Fields = append(pb.Fields, f)
	}
	rb := p.next() // '}'
	pb.End = rb.end
	return pb, nil
}

// parseTarget implements:
//
//	Target = "target" string [ "release" string ] "{" newline { Field } "}" newline .
func (p *parser) parseTarget() (*TargetBlock, error) {
	kw := p.next() // "target"
	tagTok := p.next()
	if tagTok.kind != tokString {
		return nil, p.errorf(tagTok.pos, "expected target tag string")
	}
	tb := &TargetBlock{Start: kw.pos, Tag: tagTok.text}

	if p.peek().kind == tokIdent && p.peek().text == "release" {
		p.next()
		relTok := p.next()
		if relTok.kind != tokString {
			return nil, p.errorf(relTok.pos, "expected release string")
		}
		tb.Release = relTok.text
	}

	if err := p.parseBlockOpen("target", fmt.Sprintf("%q", tagTok.text)); err != nil {
		return nil, err
	}

	var pendingBefore []Comment
	for {
		for {
			t := p.peek()
			if t.kind == tokNewline {
				p.next()
				continue
			}
			if t.kind == tokComment {
				pendingBefore = append(pendingBefore, Comment{Start: t.pos, Token: t.text})
				p.next()
				if p.peek().kind == tokNewline {
					p.next()
				}
				continue
			}
			break
		}
		if p.peek().kind == tokRBrace {
			break
		}
		if p.peek().kind == tokEOF {
			return nil, p.errorf(p.peek().pos, "unexpected EOF inside target %q block", tagTok.text)
		}
		f, err := p.parseField()
		if err != nil {
			return nil, err
		}
		f.Before = pendingBefore
		pendingBefore = nil
		if p.peek().kind == tokComment {
			c := p.next()
			f.Suffix = []Comment{{Start: c.pos, Token: c.text}}
		}
		if p.peek().kind == tokNewline {
			p.next()
		} else if p.peek().kind != tokRBrace {
			return nil, p.errorf(p.peek().pos, "expected newline after field %q", f.Key)
		}
		tb.Fields = append(tb.Fields, f)
	}
	rb := p.next()
	tb.End = rb.end
	return tb, nil
}