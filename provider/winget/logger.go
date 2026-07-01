package winget

import "github.com/vertex-language/pkg/provider"

// Logger is an alias for provider.Logger — see pkg/provider/provider.go.
// Kept as a named type in this package so existing call sites (winget.Logger)
// don't change, while every provider now shares one Logger contract.
type Logger = provider.Logger