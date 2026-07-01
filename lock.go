package pkg

import (
	"fmt"
	"path/filepath"

	"github.com/gofrs/flock"
	"github.com/vertex-language/pkg/mod"
)

// lockModule acquires an exclusive, cross-process file lock scoped to a
// single (module path, version) pair, and returns a function that
// releases it. Cache.Mod holds this for its full
// check-vs.sum -> fetch -> extract -> verify sequence, so two `vertex
// build` invocations racing to fetch the same uncached module@version
// block on each other instead of extracting into the same directory
// concurrently. Different versions of the same module never contend —
// only an exact (path, version) match does.
func (c *Cache) lockModule(mv mod.ModuleVersion) (unlock func(), err error) {
	return c.lockKey("mod", lockFileName(string(mv.Path)+"@"+mv.Version))
}

// lockLib is lockModule's analogue for a native-library install, keyed
// by the resolved-artifact hash from lib_cache.go rather than a module
// path — see libInstallKey for why the key has to be the resolved
// artifact, not whichever module happened to request it.
func (c *Cache) lockLib(hash string) (unlock func(), err error) {
	return c.lockKey("lib", hash)
}

func (c *Cache) lockKey(kind, key string) (func(), error) {
	path := filepath.Join(c.dir, "lock", kind+"-"+key+".lock")
	fl := flock.New(path)
	if err := fl.Lock(); err != nil {
		return nil, fmt.Errorf("pkg: acquire lock %s: %w", path, err)
	}
	return func() { _ = fl.Unlock() }, nil
}

// lockFileName makes an arbitrary string safe as one path component: a
// module path contains "/", which a lock file living in one flat
// directory can't. Non-alphanumeric characters collapse to "_"; a
// collision between two different inputs only costs extra serialization
// between two otherwise-unrelated locks, never correctness — whatever's
// actually being protected is re-checked after the lock is held either
// way.
func lockFileName(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}