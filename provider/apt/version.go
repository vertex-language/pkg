package apt

import "strings"

// compareVersions returns -1, 0, or 1 — same contract as strings.Compare.
// Implements the Debian version ordering algorithm from policy §5.6.12:
//
//   [epoch:]upstream[-revision]
//
// Comparison order: epoch (int) → upstream (debian string order) → revision.
func compareVersions(a, b string) int {
	ae, av, ar := splitVersion(a)
	be, bv, br := splitVersion(b)

	if c := compareInts(ae, be); c != 0 {
		return c
	}
	if c := compareDebianString(av, bv); c != 0 {
		return c
	}
	return compareDebianString(ar, br)
}

// satisfiesConstraint checks whether `available` satisfies `op version`.
// op is one of: >= <= >> << =
func satisfiesConstraint(available, op, required string) bool {
	c := compareVersions(available, required)
	switch op {
	case ">=":
		return c >= 0
	case "<=":
		return c <= 0
	case ">>":
		return c > 0
	case "<<":
		return c < 0
	case "=":
		return c == 0
	}
	return true // unknown op → don't filter
}

// splitVersion decomposes "1:2.3.4-5" into epoch, upstream, revision.
func splitVersion(v string) (epoch, upstream, revision string) {
	// epoch
	if i := strings.IndexByte(v, ':'); i >= 0 {
		epoch = v[:i]
		v = v[i+1:]
	}
	// revision (last hyphen)
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		revision = v[i+1:]
		upstream = v[:i]
	} else {
		upstream = v
	}
	return
}

// compareDebianString implements the Debian string ordering algorithm.
// It alternates between non-digit and digit chunks.
func compareDebianString(a, b string) int {
	for a != "" || b != "" {
		// non-digit part
		ac, bc := leadingNonDigit(a), leadingNonDigit(b)
		if c := compareNonDigit(ac, bc); c != 0 {
			return c
		}
		a, b = a[len(ac):], b[len(bc):]

		// digit part
		an, bn := leadingDigit(a), leadingDigit(b)
		if c := compareInts(an, bn); c != 0 {
			return c
		}
		a, b = a[len(an):], b[len(bn):]
	}
	return 0
}

func leadingNonDigit(s string) string {
	i := 0
	for i < len(s) && (s[i] < '0' || s[i] > '9') {
		i++
	}
	return s[:i]
}

func leadingDigit(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// compareNonDigit compares two non-digit strings using Debian character order:
//
//	~ < (empty) < letters < everything else (by ASCII)
func compareNonDigit(a, b string) int {
	ai, bi := 0, 0
	for ai < len(a) || bi < len(b) {
		var ac, bc int
		if ai < len(a) {
			ac = debianOrder(a[ai])
			ai++
		} else {
			ac = 0 // empty sorts after ~, before letters
		}
		if bi < len(b) {
			bc = debianOrder(b[bi])
			bi++
		} else {
			bc = 0
		}
		if ac < bc {
			return -1
		}
		if ac > bc {
			return 1
		}
	}
	return 0
}

// debianOrder maps a byte to its Debian sort weight.
//
//	~          → -1  (sorts before everything, including empty)
//	A-Z, a-z   → ASCII value (letters sort lowest among non-tilde)
//	0-9        → never reaches here (handled in digit chunks)
//	everything else → ASCII value + 256 (sorts after letters)
func debianOrder(c byte) int {
	if c == '~' {
		return -1
	}
	if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
		return int(c)
	}
	return int(c) + 256
}

// compareInts compares two decimal-string integers.
// Empty string is treated as "0".
func compareInts(a, b string) int {
	// trim leading zeros
	a = strings.TrimLeft(a, "0")
	b = strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}