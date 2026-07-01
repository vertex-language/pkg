package apt

import "github.com/vertex-language/pkg/provider"

// Params is an alias for provider.Params — see logger.go for why.
type Params = provider.Params

var _ provider.Provider = (*Apt)(nil)