package cpi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mmt-delivery/consts"
	"mmt-delivery/pkg/cf"
	"mmt-delivery/pkg/env"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gitobj "github.com/go-git/go-git/v5/plumbing/object"
	auth "github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/google/go-github/v72/github"
)

var githubClient *github.Client

var gitRepo *git.Repository

// gitAuth returns git credentials by resolving the destination from the resolver.
func gitAuth(resolver *cf.DestinationServiceClient, destName string) (*auth.BasicAuth, error) {
	dest, err := resolver.GetDestination(context.Background(), destName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve git destination '%s': %w", destName, err)
	}
	return &auth.BasicAuth{
		Username: dest.User,
		Password: dest.Password,
	}, nil
}

var Cpi_Base_Repo = "mmt-cpi-packages"
var Artifact_Base_Dir = "./artifacts"

// temporarily disable github sync
func init_disable() {
	// NOTE: This function is disabled. When re-enabled, it must receive
	// a *env.DestinationResolver and destName to resolve git credentials.
	panic("init_disable is not supported — github sync requires DestinationResolver")
}

func (c *CpiClient) SyncToGithub(resolver *cf.DestinationServiceClient, destName string, artifactId, artifactVersion, artifactType, packageID, branch, modifiedBy, modifiedAt, comment string) error {

	// Unzip the artifact content, put it to git repo
	if err := unzipSource(artifactId, artifactVersion, packageID); err != nil {
		return fmt.Errorf("failed to unzip artifact %s:%s: %w", artifactId, artifactVersion, err)
	}

	// commit changes of artifact, push to github with tag
	tag := fmt.Sprintf("%s-v%s", artifactId, artifactVersion)
	commitMessage := fmt.Sprintf(`
	%s
	------------------------------
	Sync Artifact to github.
	Artifact Type: %s
	Artifact: %s v%s
	Package: %s
	Modified By: %s   Modified At: %s
	`, comment, artifactType, artifactId, artifactVersion, packageID, modifiedBy, modifiedAt)

	// https://github.com/go-git/go-git/blob/main/_examples/tag-create-push/main.go
	// sync to github
	if err := CommitAndPushChanges(resolver, destName, fmt.Sprintf("%s/%s/", packageID, artifactId), tag, branch, modifiedBy, modifiedAt, commitMessage); err != nil {
		return fmt.Errorf("failed to commit and push changes for artifact %s:%s: %w", artifactId, artifactVersion, err)
	}

	return nil

}

// download artifact zip file, write it to the base directory
func (c *CpiClient) DownloadArtifact(ctx context.Context, artifactId, artifactVersion, packageID, artifactType string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.LongRequestTimeout)
	defer cancel()
	// Download the artifact from CPI
	fullURL := fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')/$value", c.ApiURL, artifactId, artifactVersion)
	request := env.HttpRequest{
		Method: http.MethodGet,
		ApiURL: fullURL,
	}
	artifactContent, err := c.Do(childCtx, &request) // zip content
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger().Errorf("DownloadArtifact request timeout after %v: %s", consts.LongRequestTimeout, fullURL)
		}
		return fmt.Errorf("failed to download artifact: %w", err)
	}

	// Write the artifact content to a file in the base directory
	if err := os.MkdirAll(Artifact_Base_Dir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create artifact directory %s: %w", Artifact_Base_Dir, err)
	}

	artifactFilePath := fmt.Sprintf("%s/%s:%s.zip", Artifact_Base_Dir, artifactId, artifactVersion)
	artifactFile, err := os.Create(artifactFilePath)
	if err != nil {
		return fmt.Errorf("failed to create artifact file %s: %w", artifactFilePath, err)
	}
	defer artifactFile.Close()

	if _, err := artifactFile.Write(artifactContent); err != nil {
		return fmt.Errorf("failed to write artifact content to file %s: %w", artifactFilePath, err)
	}

	return nil
}

// ABORT this feature
//
//	use sap JFrog instead. Currently cannot publish to github release, seems premission issue, also release asset is not applicable in this scenario.
//
// publish artifact to git repository release
func (c *CpiClient) PublishToGithubRelease(ctx context.Context, artifactId, artifactVersion, branch string) error {
	zipFilePath := fmt.Sprintf("%s/%s:%s.zip", Artifact_Base_Dir, artifactId, artifactVersion)
	zipFile, err := os.Open(zipFilePath)
	if err != nil {
		return fmt.Errorf("failed to open zip file %s: %w", zipFilePath, err)
	}
	defer zipFile.Close()

	fileInfo, err := zipFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info for %s: %w", zipFilePath, err)
	}

	release, _, err := githubClient.Repositories.CreateRelease(ctx, "MaCo-MMT", Cpi_Base_Repo, &github.RepositoryRelease{
		TagName:         github.String(fmt.Sprintf("%s-v%s", artifactId, artifactVersion)),
		Name:            github.String(fmt.Sprintf("%s v%s", artifactId, artifactVersion)),
		Body:            github.String(fmt.Sprintf("Artifact %s:%s\nUploaded by CPI Delivery", artifactId, artifactVersion)),
		Draft:           github.Bool(false),
		Prerelease:      github.Bool(false),
		TargetCommitish: github.String(branch), // or the branch you want to target
	})
	if err != nil {
		return fmt.Errorf("failed to create release: %w", err)
	}

	_, _, err = githubClient.Repositories.UploadReleaseAsset(ctx, "MaCo-MMT", Cpi_Base_Repo, release.GetID(), &github.UploadOptions{
		Name:  fileInfo.Name(),
		Label: fmt.Sprintf("Artifact %s:%s", artifactId, artifactVersion),
	}, zipFile)
	if err != nil {
		return fmt.Errorf("failed to upload release asset: %w", err)
	}

	return nil
}

// upload artifact zip file to respective tenant
func (c *CpiClient) UploadArtifact(ctx context.Context, artifactId string, artifactName string, artifactVersion string, packageId string) error {
	childCtx, cancel := context.WithTimeout(ctx, consts.ImportTimeout)
	defer cancel()

	// Read zip file from disk
	zipFilePath := fmt.Sprintf("%s/%s:%s.zip", Artifact_Base_Dir, artifactId, artifactVersion)
	zipFile, err := os.Open(zipFilePath)
	if err != nil {
		return fmt.Errorf("failed to open zip file %s: %w", zipFilePath, err)
	}
	defer zipFile.Close()

	// Read the file content into a buffer
	zipBuffer := new(bytes.Buffer)
	if _, err := io.Copy(zipBuffer, zipFile); err != nil {
		return fmt.Errorf("failed to read zip file %s: %w", zipFilePath, err)
	}
	// Encode zipBuffer with base64
	encodedZipBuffer := base64.StdEncoding.EncodeToString(zipBuffer.Bytes())

	// Create payload
	payload := map[string]any{
		"Name":            artifactName,
		"Id":              artifactId,
		"PackageId":       packageId,
		"ArtifactContent": encodedZipBuffer,
	}

	// Encode payload to JSON
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	request := env.HttpRequest{
		Method:      http.MethodPost,
		ApiURL:      fmt.Sprintf("%s/IntegrationDesigntimeArtifacts", c.ApiURL),
		RequestBody: bytes.NewBuffer(requestBody),
	}
	response, err := c.Do(childCtx, &request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			logger().Errorf("UploadArtifact request timeout after %v: %s/IntegrationDesigntimeArtifacts", consts.ImportTimeout, c.ApiURL)
		}
		return fmt.Errorf("error while uploading artifact: %s", err)
	}
	logger().Infof("Artifact %s:%s uploaded successfully, response: %s", artifactId, artifactVersion, string(response))
	return nil
}

func zipSource(source, target string) (*bytes.Buffer, error) {
	// 1. Create a ZIP file and zip.Writer
	buffer := new(bytes.Buffer)
	writer := zip.NewWriter(buffer)
	defer writer.Close()

	// 2. Go through all the files of the source
	return buffer, filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 3. Create a local file header
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		// set compression
		header.Method = zip.Deflate

		// 4. Set relative path of a file as the header name
		header.Name, err = filepath.Rel(filepath.Dir(source), path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			header.Name += "/"
		}

		// 5. Create writer for the file header and save content of the file
		headerWriter, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(headerWriter, f)
		return err
	})
}

// unzip Artifact zip file, write it to respective package directory
func unzipSource(artifactId, artifactVersion, packageId string) error {
	source := fmt.Sprintf("%s/%s:%s.zip", Artifact_Base_Dir, artifactId, artifactVersion) // zip artifact file path
	zipReader, err := zip.OpenReader(source)
	if err != nil {
		return fmt.Errorf("failed to open zip file %s: %w", source, err)
	}

	// Write files to the current directory
	for _, zippedFile := range zipReader.File {
		artifactBaseDir := fmt.Sprintf("%s/%s/%s", Cpi_Base_Repo, packageId, artifactId)
		targetPath := filepath.Join(artifactBaseDir, zippedFile.Name)

		// Create directories if necessary
		if zippedFile.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, os.ModePerm); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", targetPath, err)
			}
			continue
		}

		// Ensure the parent directory exists
		if err := os.MkdirAll(filepath.Dir(targetPath), os.ModePerm); err != nil {
			return fmt.Errorf("failed to create parent directory for %s: %w", targetPath, err)
		}

		// create target file
		targetFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, zippedFile.Mode())
		if err != nil {
			return fmt.Errorf("failed to create file %s: %w", targetPath, err)
		}
		defer targetFile.Close()

		// Write the file content
		rc, err := zippedFile.Open()
		if err != nil {
			return fmt.Errorf("failed to open file %s in zip: %w", zippedFile.Name, err)
		}
		defer rc.Close()

		if _, err := io.Copy(targetFile, rc); err != nil {
			return fmt.Errorf("failed to write file %s: %w", targetPath, err)
		}
	}
	return nil
}

// path: relative path of artifact from package directory
func CommitAndPushChanges(resolver *cf.DestinationServiceClient, destName string, path string, tag string, branch, modifiedBy, modifiedAt, commitMessage string) error {
	worktree, err := gitRepo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// checkout to cpi instance corresponding branch
	branchRef := plumbing.NewBranchReferenceName(branch)
	_, err = gitRepo.Reference(branchRef, true)
	if err == plumbing.ErrReferenceNotFound {
		if err := worktree.Checkout(&git.CheckoutOptions{
			Branch: branchRef,
			Create: true,
			Keep:   true,
		}); err != nil {
			return fmt.Errorf("failed to create and checkout branch %s: %w", branch, err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check branch existence: %w", err)
	}
	// If the branch exists, checkout to it
	if err := worktree.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Keep:   true,
	}); err != nil {
		return fmt.Errorf("failed to checkout branch %s: %w", branch, err)
	}

	//
	if err := worktree.AddWithOptions(&git.AddOptions{
		// All:  true,
		Path: path,
	}); err != nil {
		return fmt.Errorf("failed to add changes: %w", err)
	}

	// Commit the changes
	var commitId plumbing.Hash
	if commitId, err = worktree.Commit(commitMessage, &git.CommitOptions{
		Author: &gitobj.Signature{
			Name:  modifiedBy,
			Email: modifiedBy,
			When:  time.Now(),
		},
	}); err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	if _, err = gitRepo.CreateTag(tag, commitId, &git.CreateTagOptions{
		Message: "tag is :" + tag,
	}); err != nil {
		return fmt.Errorf("failed to create tag %s: %w", tag, err)
	}

	// Push the changes
	gitCreds, err := gitAuth(resolver, destName)
	if err != nil {
		return fmt.Errorf("failed to resolve git credentials: %w", err)
	}
	if err := gitRepo.Push(&git.PushOptions{
		Auth:       gitCreds,
		FollowTags: true,
		Force:      true,
	}); err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}

	return nil
}
