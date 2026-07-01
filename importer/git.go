package importer

import (
	"fmt"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/vertex-language/pkg/mod"
)

type gitFetcher struct{}

func (gitFetcher) List(path mod.ModulePath) ([]string, error) {
	refs, err := listRemoteRefs(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range refs {
		if r.Name().IsTag() && isCanonicalVersion(r.Name().Short()) {
			out = append(out, r.Name().Short())
		}
	}
	sortVersions(out)
	return out, nil
}

func (gitFetcher) Resolve(path mod.ModulePath, query string) (string, error) {
	refs, err := listRemoteRefs(path)
	if err != nil {
		return "", err
	}

	if query == "" || query == "latest" {
		return resolveLatest(path, refs)
	}

	if isCanonicalVersion(query) {
		for _, r := range refs {
			if r.Name().IsTag() && r.Name().Short() == query {
				return query, nil
			}
		}
		return "", errf("%s: tag %q not found", path, query)
	}

	// A named tag or branch that isn't itself a canonical version
	// (e.g. "v1", "main", "feature/x") resolves to a pseudo-version
	// pinned at that ref's current commit.
	for _, r := range refs {
		if !r.Name().IsTag() && !r.Name().IsBranch() {
			continue
		}
		if r.Name().Short() != query {
			continue
		}
		hash, t, err := commitInfo(path, r.Hash())
		if err != nil {
			return "", err
		}
		return pseudoVersion(t, hash), nil
	}

	if isFullHash(query) {
		hash, t, err := commitInfo(path, plumbing.NewHash(query))
		if err != nil {
			return "", errf("%s: %q is not a known tag, branch, or reachable commit: %v", path, query, err)
		}
		return pseudoVersion(t, hash), nil
	}

	return "", errf("%s: %q is not a known tag, branch, or full commit hash", path, query)
}

func (gitFetcher) Fetch(path mod.ModulePath, version string, dir string) error {
	url, err := repoURL(path)
	if err != nil {
		return err
	}

	if hash, ok := pseudoVersionHash(version); ok {
		return fetchCommit(url, hash, dir)
	}
	if !isCanonicalVersion(version) {
		return errf("%s@%s: not a value Resolve could have returned", path, version)
	}
	return fetchTag(url, version, dir)
}

// listRemoteRefs lists a remote's refs without cloning anything —
// go-git's Remote.List talks the git wire protocol directly.
func listRemoteRefs(path mod.ModulePath) ([]*plumbing.Reference, error) {
	url, err := repoURL(path)
	if err != nil {
		return nil, err
	}
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{url},
	})
	refs, err := remote.List(&git.ListOptions{})
	if err != nil {
		return nil, errf("list refs for %s: %v", path, err)
	}
	return refs, nil
}

func resolveLatest(path mod.ModulePath, refs []*plumbing.Reference) (string, error) {
	var tags []string
	for _, r := range refs {
		if r.Name().IsTag() && isCanonicalVersion(r.Name().Short()) {
			tags = append(tags, r.Name().Short())
		}
	}
	if len(tags) > 0 {
		sortVersions(tags)
		return tags[len(tags)-1], nil
	}

	head, err := defaultBranchHead(refs)
	if err != nil {
		return "", errf("%s: no tags found and %v", path, err)
	}
	hash, t, err := commitInfo(path, head)
	if err != nil {
		return "", err
	}
	return pseudoVersion(t, hash), nil
}

func defaultBranchHead(refs []*plumbing.Reference) (plumbing.Hash, error) {
	for _, r := range refs {
		if r.Name() == plumbing.HEAD && r.Type() == plumbing.HashReference {
			return r.Hash(), nil
		}
	}
	for _, name := range []string{"main", "master"} {
		for _, r := range refs {
			if r.Name().IsBranch() && r.Name().Short() == name {
				return r.Hash(), nil
			}
		}
	}
	return plumbing.ZeroHash, fmt.Errorf("no default branch (HEAD/main/master) found")
}

// commitInfo fetches a single commit object (not its history) and
// returns its hash string and author time.
//
// Requires the remote to support fetching by exact commit hash over
// smart HTTP (uploadpack.allowReachableSHA1InWant) — GitHub, GitLab, and
// Bitbucket Cloud all allow this for public repos. A self-hosted server
// with it disabled will surface as a plain fetch error here rather than
// being special-cased, consistent with this package's other documented
// gaps.
func commitInfo(path mod.ModulePath, hash plumbing.Hash) (string, time.Time, error) {
	url, err := repoURL(path)
	if err != nil {
		return "", time.Time{}, err
	}

	repo, err := git.Init(memory.NewStorage(), nil)
	if err != nil {
		return "", time.Time{}, errf("%s: init in-memory repo: %v", path, err)
	}
	remote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}})
	if err != nil {
		return "", time.Time{}, errf("%s: create remote: %v", path, err)
	}

	spec := config.RefSpec(hash.String() + ":refs/vertex/pin")
	if err := remote.Fetch(&git.FetchOptions{RefSpecs: []config.RefSpec{spec}, Depth: 1}); err != nil {
		return "", time.Time{}, errf("%s: fetch commit %s: %v", path, hash, err)
	}

	commit, err := repo.CommitObject(hash)
	if err != nil {
		return "", time.Time{}, errf("%s: read commit %s: %v", path, hash, err)
	}
	return hash.String(), commit.Author.When, nil
}

// fetchTag shallow-clones exactly one tag's tip — the common case, and
// the cheap one: one commit's tree, no history.
func fetchTag(url, tag, dir string) error {
	_, err := git.PlainClone(dir, false, &git.CloneOptions{
		URL:           url,
		ReferenceName: plumbing.NewTagReferenceName(tag),
		SingleBranch:  true,
		Depth:         1,
		Tags:          git.NoTags,
	})
	if err != nil {
		return errf("clone %s@%s: %v", url, tag, err)
	}
	return nil
}

// fetchCommit checks out an exact commit that isn't necessarily the tip
// of any branch or tag. A shallow clone can only follow a named ref, so
// this instead asks the remote for that one object directly (same
// mechanism as commitInfo) and checks it out into dir's worktree.
func fetchCommit(url, hash, dir string) error {
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		return errf("init %s: %v", dir, err)
	}
	remote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}})
	if err != nil {
		return errf("create remote: %v", err)
	}

	spec := config.RefSpec(hash + ":refs/vertex/pin")
	if err := remote.Fetch(&git.FetchOptions{RefSpecs: []config.RefSpec{spec}, Depth: 1}); err != nil {
		return errf("fetch commit %s: %v", hash, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return errf("worktree: %v", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(hash)}); err != nil {
		return errf("checkout %s: %v", hash, err)
	}
	return nil
}