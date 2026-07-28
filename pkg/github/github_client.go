package github

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/google/go-github/v89/github"

	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/env"
)

// Git tree entry file modes
const fileModeBlob = "100644" // regular non-executable file

// Compile-time interface compliance
var _ GitArtifactClient = (*GoGitHubClient)(nil)

// GoGitHubClient implements GitArtifactClient using GitHub REST API v3 (via go-github).
// Works identically for github.com and GitHub Enterprise Server — only the base URL differs.
type GoGitHubClient struct {
	client *github.Client
	owner  string
	repo   string
}

// newGoGitHubClient creates a GoGitHubClient by resolving a BTP Destination for auth/URL.
// The destination must be BasicAuthentication type with Password = GitHub PAT.
func newGoGitHubClient(ctx context.Context, destName string, owner, repo string, resolver *cf.DestinationServiceClient) (*GoGitHubClient, error) {
	dest, err := resolver.GetDestination(ctx, destName)
	if err != nil {
		return nil, fmt.Errorf("github destination %s not found: %w", destName, err)
	}

	token := dest.Password
	if token == "" {
		return nil, fmt.Errorf("github destination %s has no password (PAT token)", destName)
	}

	// Build client options
	opts := []github.ClientOptionsFunc{github.WithAuthToken(token)}

	// For GHE Server, override the base URL (destination URL should point to HOSTNAME)
	apiURL := normalizeGitURL(dest.URL)
	if apiURL != "" && apiURL != "https://api.github.com" {
		opts = append(opts, github.WithEnterpriseURLs(apiURL, apiURL))
	}

	client, err := github.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create github client: %w", err)
	}

	return &GoGitHubClient{client: client, owner: owner, repo: repo}, nil
}

func (g *GoGitHubClient) TagExists(ctx context.Context, tag string) (bool, string, error) {
	ref, resp, err := g.client.Git.GetRef(ctx, g.owner, g.repo, "tags/"+tag)
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return false, "", nil
		}
		return false, "", fmt.Errorf("TagExists(%s): %w", tag, err)
	}
	return true, ref.GetObject().GetSHA(), nil
}

func (g *GoGitHubClient) Commit(ctx context.Context, branch string, treePath string, files FileMap, meta CommitMeta) (string, error) {
	// Ensure branch exists (auto-create from default branch if not)
	if err := g.ensureBranch(ctx, branch); err != nil {
		return "", err
	}

	// 1. Get current branch tip
	branchRef, _, err := g.client.Git.GetRef(ctx, g.owner, g.repo, "heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("get branch ref %s: %w", branch, err)
	}
	parentSHA := branchRef.GetObject().GetSHA()

	// 2. Get the parent commit to find base tree
	parentCommit, _, err := g.client.Git.GetCommit(ctx, g.owner, g.repo, parentSHA)
	if err != nil {
		return "", fmt.Errorf("get parent commit: %w", err)
	}
	baseTreeSHA := parentCommit.GetTree().GetSHA()

	// 3. Create blobs and build tree entries
	var entries []*github.TreeEntry
	for relPath, content := range files {
		fullPath := path.Join(treePath, relPath)
		blob, _, err := g.client.Git.CreateBlob(ctx, g.owner, g.repo, github.Blob{
			Content:  github.Ptr(base64.StdEncoding.EncodeToString(content)),
			Encoding: github.Ptr("base64"),
		})
		if err != nil {
			return "", fmt.Errorf("create blob for %s: %w", relPath, err)
		}
		entries = append(entries, &github.TreeEntry{
			Path: github.Ptr(fullPath),
			Mode: github.Ptr(fileModeBlob),
			Type: github.Ptr("blob"),
			SHA:  blob.SHA,
		})
	}

	// 4. Create tree (with base_tree to preserve files outside treePath)
	newTree, _, err := g.client.Git.CreateTree(ctx, g.owner, g.repo, baseTreeSHA, entries)
	if err != nil {
		return "", fmt.Errorf("create tree: %w", err)
	}

	// 5. If tree SHA unchanged, skip commit (idempotent for retry scenarios)
	if newTree.GetSHA() == parentCommit.GetTree().GetSHA() {
		env.L(ctx).Infow("tree unchanged, skipping commit", "branch", branch, "treePath", treePath)
		return parentSHA, nil
	}

	// 6. Create commit
	newCommit, _, err := g.client.Git.CreateCommit(ctx, g.owner, g.repo, github.Commit{
		Message: github.Ptr(meta.Message),
		Tree:    newTree,
		Parents: []*github.Commit{{SHA: github.Ptr(parentSHA)}},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("create commit: %w", err)
	}

	// 7. Update branch ref
	_, _, err = g.client.Git.UpdateRef(ctx, g.owner, g.repo, "heads/"+branch, github.UpdateRef{
		SHA: newCommit.GetSHA(),
	})
	if err != nil {
		return "", fmt.Errorf("update ref %s: %w", branch, err)
	}

	return newCommit.GetSHA(), nil
}

// emptyTreeSHA computes the SHA-1 of an empty Git tree object at init time.
//
// This is a well-known constant in the Git ecosystem, hardcoded in Git's own source:
// https://github.com/git/git/blob/master/hash.c (search "empty_tree_oid")
//
//	static const struct object_id empty_tree_oid = {
//	    .hash = { 0x4b, 0x82, 0x5d, 0xc6, 0x42, 0xcb, 0x6e, 0xb9, ... },
//	    .algo = GIT_HASH_SHA1,
//	};
//
// The value is deterministic: SHA-1("tree 0\0") = 4b825dc642cb6eb9a060e54bf8d69288fbee4904.
// It exists in every SHA-1 Git repository and is used by Git internally for operations like
// `git diff --cached` against an empty tree (pre-commit hooks), orphan branch creation, etc.
//
// Community discussion: https://stackoverflow.com/questions/9765453
// Git source reference: https://github.com/git/git/blob/master/hash.c
var emptyTreeSHA = computeEmptyTreeSHA()

func computeEmptyTreeSHA() string {
	h := sha1.Sum([]byte("tree 0\x00"))
	return hex.EncodeToString(h[:])
}

// ensureBranch checks if a branch exists; if not, creates an orphan branch with an empty tree.
// This ensures tenant branches contain only artifacts synced to that tenant, without inheriting
// files from the default branch.
func (g *GoGitHubClient) ensureBranch(ctx context.Context, branch string) error {
	_, resp, err := g.client.Git.GetRef(ctx, g.owner, g.repo, "heads/"+branch)
	if err == nil {
		return nil // branch exists
	}
	if resp == nil || resp.StatusCode != 404 {
		return fmt.Errorf("check branch %s: %w", branch, err)
	}

	// Create root commit with empty tree (orphan — no parents)
	initCommit, _, err := g.client.Git.CreateCommit(ctx, g.owner, g.repo, github.Commit{
		Message: github.Ptr("init: " + branch),
		Tree:    &github.Tree{SHA: github.Ptr(emptyTreeSHA)},
		Parents: []*github.Commit{},
	}, nil)
	if err != nil {
		return fmt.Errorf("create orphan commit for %s: %w", branch, err)
	}

	_, _, err = g.client.Git.CreateRef(ctx, g.owner, g.repo, github.CreateRef{
		Ref: "refs/heads/" + branch,
		SHA: initCommit.GetSHA(),
	})
	if err != nil {
		return fmt.Errorf("create branch %s: %w", branch, err)
	}
	return nil
}

func (g *GoGitHubClient) CreateTag(ctx context.Context, tag string, commitSHA string) error {
	_, _, err := g.client.Git.CreateRef(ctx, g.owner, g.repo, github.CreateRef{
		Ref: "refs/tags/" + tag,
		SHA: commitSHA,
	})
	if err != nil {
		return fmt.Errorf("CreateTag(%s): %w", tag, err)
	}
	return nil
}

// ListOwners returns the authenticated user and all organizations they belong to.
func (g *GoGitHubClient) ListOwners(ctx context.Context) ([]OwnerInfo, error) {
	var owners []OwnerInfo

	// Get authenticated user
	user, _, err := g.client.Users.Get(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("get authenticated user: %w", err)
	}
	owners = append(owners, OwnerInfo{Login: user.GetLogin(), Type: "User"})

	// List all user's organizations (paginated)
	opts := &github.ListOptions{PerPage: 100}
	for {
		orgs, resp, err := g.client.Organizations.List(ctx, "", opts)
		if err != nil {
			return nil, fmt.Errorf("list organizations: %w", err)
		}
		for _, org := range orgs {
			owners = append(owners, OwnerInfo{Login: org.GetLogin(), Type: "Organization"})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return owners, nil
}

// ListRepos returns repositories for the given owner.
// ownerType ("User" or "Organization") determines which API to call, avoiding an extra GET /user round-trip.
func (g *GoGitHubClient) ListRepos(ctx context.Context, owner string, ownerType string) ([]RepoInfo, error) {
	var repos []RepoInfo

	if ownerType == "User" {
		opts := &github.RepositoryListByAuthenticatedUserOptions{ListOptions: github.ListOptions{PerPage: 100}}
		for {
			ghRepos, resp, err := g.client.Repositories.ListByAuthenticatedUser(ctx, opts)
			if err != nil {
				return nil, fmt.Errorf("list user repos: %w", err)
			}
			for _, r := range ghRepos {
				repos = append(repos, RepoInfo{Name: r.GetName(), FullName: r.GetFullName(), Private: r.GetPrivate()})
			}
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	} else {
		opts := &github.RepositoryListByOrgOptions{ListOptions: github.ListOptions{PerPage: 100}}
		for {
			ghRepos, resp, err := g.client.Repositories.ListByOrg(ctx, owner, opts)
			if err != nil {
				return nil, fmt.Errorf("list org %s repos: %w", owner, err)
			}
			for _, r := range ghRepos {
				repos = append(repos, RepoInfo{Name: r.GetName(), FullName: r.GetFullName(), Private: r.GetPrivate()})
			}
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}

	return repos, nil
}

func (g *GoGitHubClient) ReadTree(ctx context.Context, commitSHA string, treePath string) (FileMap, error) {
	// Get the tree at commitSHA recursively
	tree, _, err := g.client.Git.GetTree(ctx, g.owner, g.repo, commitSHA, true)
	if err != nil {
		return nil, fmt.Errorf("get tree at %s: %w", commitSHA, err)
	}

	files := make(FileMap)
	prefix := treePath + "/"

	for _, entry := range tree.Entries {
		entryPath := entry.GetPath()
		if !strings.HasPrefix(entryPath, prefix) {
			continue
		}
		if entry.GetType() != "blob" {
			continue
		}

		// Fetch blob content
		blob, _, err := g.client.Git.GetBlob(ctx, g.owner, g.repo, entry.GetSHA())
		if err != nil {
			return nil, fmt.Errorf("get blob %s: %w", entryPath, err)
		}

		var content []byte
		switch blob.GetEncoding() {
		case "base64":
			content, err = base64.StdEncoding.DecodeString(blob.GetContent())
			if err != nil {
				return nil, fmt.Errorf("decode blob %s: %w", entryPath, err)
			}
		case "utf-8", "":
			content = []byte(blob.GetContent())
		default:
			return nil, fmt.Errorf("unsupported blob encoding %s for %s", blob.GetEncoding(), entryPath)
		}

		relPath := strings.TrimPrefix(entryPath, prefix)
		files[relPath] = content
	}

	return files, nil
}

// normalizeGitURL parses a destination URL and forces HTTPS scheme.
// GitHub API (both github.com and GHE Server) never uses plain HTTP.
func normalizeGitURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		parsed.Scheme = "https"
		return strings.TrimRight(parsed.String(), "/")
	}
	return strings.TrimRight(raw, "/")
}
