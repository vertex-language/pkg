package brew

import "github.com/vertex-language/pkg/provider"

// Params is an alias for provider.Params.
type Params = provider.Params

var _ provider.Provider = (*Brew)(nil)