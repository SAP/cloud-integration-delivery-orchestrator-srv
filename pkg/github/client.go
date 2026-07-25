package github

import "context"

// FileMap represents a set of files: relative path → content bytes.
type FileMap map[string][]byte

// CommitMeta holds metadata for creating a commit.
type CommitMeta struct {
	Message string
}

// GitArtifactClient abstracts Git operations for artifact sync and compare.
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
}
