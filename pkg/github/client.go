package github

import (
	"context"
	"fmt"

	"mmt-delivery/pkg/cf"
)

// Provider identifies a Git hosting provider.
type Provider string

const ProviderGitHub Provider = "github"

// SupportedProviders returns all providers the system can create clients for.
func SupportedProviders() []Provider {
	return []Provider{ProviderGitHub}
}

// FileMap represents a set of files: relative path → content bytes.
type FileMap map[string][]byte

// CommitMeta holds metadata for creating a commit.
type CommitMeta struct {
	Message string
}

// OwnerInfo describes a GitHub user or organization returned by discovery.
type OwnerInfo struct {
	Login string `json:"login"`
	Type  string `json:"type"` // "User" or "Organization"
}

// RepoInfo describes a GitHub repository returned by discovery.
type RepoInfo struct {
	Name     string `json:"name"`
	FullName string `json:"fullName"`
	Private  bool   `json:"private"`
}

// GitArtifactClient abstracts Git operations for artifact sync, compare, and discovery.
// The interface is provider-agnostic — implementations exist for GitHub (current)
// and can be added for GitLab/Bitbucket in the future.
type GitArtifactClient interface {
	// TagExists checks whether a tag ref exists and returns its commit SHA if so.
	TagExists(ctx context.Context, tag string) (exists bool, commitSHA string, err error)

	// Commit writes a set of files to the given branch at treePath, creating a single commit.
	// Returns the new commit SHA.
	Commit(ctx context.Context, branch string, treePath string, files FileMap, meta CommitMeta) (commitSHA string, err error)

	// CreateTag creates a lightweight tag pointing to the given commit SHA.
	CreateTag(ctx context.Context, tag string, commitSHA string) error

	// ReadTree reads all files under treePath at the given commit SHA.
	// Used by the Code Compare API to serve file content to the frontend.
	ReadTree(ctx context.Context, commitSHA string, treePath string) (FileMap, error)

	// ListOwners returns the authenticated user and all organizations they belong to.
	// Used by the system config UI to populate the owner dropdown.
	ListOwners(ctx context.Context) ([]OwnerInfo, error)

	// ListRepos returns repositories accessible to the authenticated user under the given owner.
	// ownerType should be "User" or "Organization" (from ListOwners result) to avoid an extra API call.
	ListRepos(ctx context.Context, owner string, ownerType string) ([]RepoInfo, error)
}

// NewGitClient is the factory that creates a GitArtifactClient based on provider type.
// All callers must go through this factory — never instantiate provider-specific clients directly.
// For discovery-only usage (ListOwners/ListRepos), pass empty owner and repo.
func NewGitClient(ctx context.Context, provider Provider, destName, owner, repo string, resolver *cf.DestinationServiceClient) (GitArtifactClient, error) {
	switch provider {
	case ProviderGitHub:
		return newGoGitHubClient(ctx, destName, owner, repo, resolver)
	default:
		return nil, fmt.Errorf("unsupported git provider: %q", provider)
	}
}
