package mod

import "strings"

// Format renders f's Syntax tree back to canonical vs.mod text.
func Format(fs *FileSyntax) []byte {
	var b strings.Builder
	for _, x := range fs.Stmt {
		switch x := x.(type) {
		case *Line:
			printComments(&b, x.Comments.Before)
			b.WriteString(joinTokens(x.Token))
			printSuffix(&b, x.Comments.Suffix)
			b.WriteByte('\n')
		case *LineBlock:
			printComments(&b, x.Comments.Before)
			b.WriteString(x.Token[0])
			b.WriteString(" (\n")
			for _, l := range x.Line {
				printComments(&b, l.Comments.Before)
				b.WriteString("\t")
				b.WriteString(joinTokens(l.Token))
				printSuffix(&b, l.Comments.Suffix)
				b.WriteByte('\n')
			}
			b.WriteString(")\n")
		}
	}
	return []byte(b.String())
}

func joinTokens(tok []string) string {
	out := make([]string, len(tok))
	for i, t := range tok {
		out[i] = AutoQuote(t)
	}
	return strings.Join(out, " ")
}

func printComments(b *strings.Builder, cs []Comment) {
	for _, c := range cs {
		b.WriteString("//")
		if c.Token != "" {
			b.WriteByte(' ')
			b.WriteString(c.Token)
		}
		b.WriteByte('\n')
	}
}

func printSuffix(b *strings.Builder, cs []Comment) {
	for _, c := range cs {
		b.WriteString(" //")
		if c.Token != "" {
			b.WriteByte(' ')
			b.WriteString(c.Token)
		}
	}
}

// AutoQuote returns s, or s double-quoted if it contains characters
// that would otherwise break vs.mod's line-oriented token grammar.
func AutoQuote(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"`()\n") {
		q := strings.ReplaceAll(s, `\`, `\\`)
		q = strings.ReplaceAll(q, `"`, `\"`)
		return `"` + q + `"`
	}
	return s
}