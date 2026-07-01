// Package mod implements a parser and formatter for vs.mod files.
//
// vs.mod's grammar is specified directly by vs.lib §2, which this
// package tracks directive-for-directive. The compiler-version
// directive is named "vertex" (vs.lib §2 calls this "the module's
// own compiler-version line"), and the dependency directive is
// named "dependencies". Every other keyword — module, exclude,
// replace, retract, tool, ignore, toolchain — is standard
// module-file vocabulary and needed no renaming.
//
// Parse and ParseLax both parse a vs.mod file and return an
// interpreted *File. ParseLax ignores directives it doesn't
// recognize, tolerating a file written against a newer toolchain.
//
// Format renders a *File's Syntax tree back to canonical vs.mod text.
package mod