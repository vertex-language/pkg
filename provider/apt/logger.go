package apt

import "github.com/vertex-language/pkg/provider"

// Logger is an alias for provider.Logger. Kept as a named type in this
// package so existing call sites (apt.Logger) don't change, while every
// provider now shares one Logger contract — no bridging needed when a
// caller hands the same logger to apt, brew, and vcpkg at once.
type Logger = provider.Logger