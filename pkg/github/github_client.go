package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strings"

	"github.com/google/go-github/v89/github"

	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/env"
)

// GoGitHubClient implements GitArtifactClient using GitHub REST API v3 (via go-github).
// Works identically for github.com and GitHub Enterprise Server — only the base URL differs.
type GoGitHubClient struct {
	client *github.Client
	owner  string
	repo   string
}

// NewGoGitHubClient creates a GoGitHubClient by resolving a BTP Destination for auth/URL.
// The destination must be BasicAuthentication type with Password = GitHub PAT.
func NewGoGitHubClient(ctx context.Context, destName string, owner, repo string, resolver *cf.DestinationServiceClient) (*GoGitHubClient, error) {
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

	// For GHE Server, override the base URL (destination URL = "https://HOSTNAME/api/v3/")
	apiURL := strings.TrimRight(dest.URL, "/")
	if apiURL != "" && apiURL != "https://api.github.com" {
		if !strings.HasSuffix(apiURL, "/") {
			apiURL += "/"
		}
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
			Mode: github.Ptr("100644"),
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
