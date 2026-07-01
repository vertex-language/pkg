package lib

import "bytes"

// Format renders a FileSyntax tree back into canonical vs.lib text,
// including comments — the vs.lib analogue of mod.Format.
func Format(fs *FileSyntax) []byte {
	var buf bytes.Buffer
	for _, stmt := range fs.Stmt {
		printComments(&buf, stmt.comments().Before)
		switch x := stmt.(type) {
		case *LibraryLine:
			printLibrary(&buf, x)
		case *FieldLine:
			printField(&buf, 0, x)
		case *ProviderBlock:
			printProvider(&buf, x)
		}
		printSuffix(&buf, stmt.comments().Suffix)
	}
	return buf.Bytes()
}

func printComments(buf *bytes.Buffer, cs []Comment) {
	for _, c := range cs {
		buf.WriteString("//")
		buf.WriteString(c.Token)
		buf.WriteByte('\n')
	}
}

func printSuffix(buf *bytes.Buffer, cs []Comment) {
	if len(cs) > 0 {
		buf.WriteString(" //")
		buf.WriteString(cs[0].Token)
	}
	buf.WriteByte('\n')
}

func indent(buf *bytes.Buffer, depth int) {
	for i := 0; i < depth; i++ {
		buf.WriteByte('\t')
	}
}

// printLibrary renders `library <import-path>` — bare, unquoted, no
// "=" — unlike printField.
func printLibrary(buf *bytes.Buffer, l *LibraryLine) {
	buf.WriteString("library ")
	buf.WriteString(l.Path)
}

func printField(buf *bytes.Buffer, depth int, f *FieldLine) {
	indent(buf, depth)
	buf.WriteString(f.Key)
	buf.WriteString(" = ")
	buf.WriteString(AutoQuote(f.Value))
}

func printProvider(buf *bytes.Buffer, pb *ProviderBlock) {
	buf.WriteString("provider ")
	buf.WriteString(pb.Kind)
	buf.WriteString(" {\n")
	for _, f := range pb.Fields {
		printComments(buf, f.Before)
		printField(buf, 1, f)
		printSuffix(buf, f.Suffix)
	}
	for _, t := range pb.Targets {
		printComments(buf, t.Before)
		printTarget(buf, t)
		printSuffix(buf, t.Suffix)
	}
	buf.WriteString("}")
}

func printTarget(buf *bytes.Buffer, tb *TargetBlock) {
	indent(buf, 1)
	buf.WriteString("target ")
	buf.WriteString(AutoQuote(tb.Tag))
	if tb.Release != "" {
		buf.WriteString(" release ")
		buf.WriteString(AutoQuote(tb.Release))
	}
	buf.WriteString(" {\n")
	for _, f := range tb.Fields {
		printComments(buf, f.Before)
		printField(buf, 2, f)
		printSuffix(buf, f.Suffix)
	}
	indent(buf, 1)
	buf.WriteString("}")
}

// AutoQuote renders s as a double-quoted vs.lib string literal.
func AutoQuote(s string) string {
	var b bytes.Buffer
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}