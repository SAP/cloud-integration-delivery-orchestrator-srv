package cpi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mmt-delivery/env"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gitobj "github.com/go-git/go-git/v5/plumbing/object"
	auth "github.com/go-git/go-git/v5/plumbing/transport/http"
)

var githubRepo *git.Repository
var githubAuth = &auth.BasicAuth{
	Username: env.Destinations()["API_GIT_MMT_SCC"].User,
	Password: env.Destinations()["API_GIT_MMT_SCC"].Password,
}
var CPI_BASE_REPO = "mmt-cpi-packages"

func (c *CpiClient) SyncToGithub(artifactId, artifactVersion, packageID string) error {
	// download artifact
	if err := c.downloadArtifact(artifactId, artifactVersion, packageID); err != nil {
		return err
	}

	// Unzip the artifact content, put it to git repo
	if err := unzipSource(artifactId, artifactVersion, packageID); err != nil {
		return fmt.Errorf("failed to unzip artifact %s:%s: %w", artifactId, artifactVersion, err)
	}

	// commit changes of artifact, push to github with tag
	branch := "cpi-mmt-dev" // use btp subdomain as branch name
	tag := fmt.Sprintf("%s-v%s", artifactId, artifactVersion)
	artifactType := "IntegrationFlow"
	commitMessage := fmt.Sprintf(`
	Sync Artifact to github.
	Artifact Type: %s
	Artifact: %s v%s
	Package: %s
	Modified By: %s   Modified At: %s
	`, artifactType, artifactId, artifactVersion, packageID, "Doug Liu", "???")

	if err := CommitAndPushChanges(fmt.Sprintf("%s/%s/", packageID, artifactId), tag, branch, commitMessage); err != nil {
		return fmt.Errorf("failed to commit and push changes for artifact %s:%s: %w", artifactId, artifactVersion, err)
	}

	// TODO: also upload original zip file to github repo
	return nil

}
func (c *CpiClient) downloadArtifact(artifactId, artifactVersion, packageID string) error {
	childCtx, cancel := context.WithCancel(c.Context)
	defer cancel()
	// Download the artifact from CPI
	request := env.HttpRequest{
		Ctx:    childCtx,
		Method: http.MethodGet,
		ApiURL: fmt.Sprintf("%s/IntegrationDesigntimeArtifacts(Id='%s',Version='%s')/$value", c.ApiURL, artifactId, artifactVersion),
	}
	artifactContent, err := c.Do(&request)
	if err != nil {
		return fmt.Errorf("failed to download artifact: %w", err)
	}

	// Write the artifact content to a file in the base directory
	artifactFilePath := fmt.Sprintf("%s:%s.zip", artifactId, artifactVersion)
	artifactFile, err := os.Create(artifactFilePath)
	if err != nil {
		return fmt.Errorf("failed to create artifact file %s: %w", artifactFilePath, err)
	}
	defer artifactFile.Close()

	if _, err := artifactFile.Write(*artifactContent); err != nil {
		return fmt.Errorf("failed to write artifact content to file %s: %w", artifactFilePath, err)
	}

	return nil
}
func (c *CpiClient) UploadArtifact(artifactId string, artifactName string, artifactVersion string, packageId string) {

	// Read zip file from disk
	zipFilePath := fmt.Sprintf("%s:%s.zip", artifactId, artifactVersion)
	zipFile, err := os.Open(zipFilePath)
	if err != nil {
		panic(fmt.Errorf("failed to open zip file %s: %w", zipFilePath, err))
	}
	defer zipFile.Close()

	// Read the file content into a buffer
	zipBuffer := new(bytes.Buffer)
	if _, err := io.Copy(zipBuffer, zipFile); err != nil {
		panic(fmt.Errorf("failed to read zip file %s: %w", zipFilePath, err))
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
		panic(fmt.Errorf("failed to marshal payload: %w", err))
	}
	request := env.HttpRequest{
		Ctx:         c.Context,
		Method:      http.MethodPost,
		ApiURL:      fmt.Sprintf("%s/IntegrationDesigntimeArtifacts", c.ApiURL),
		RequestBody: bytes.NewBuffer(requestBody),
	}
	response, err := c.Do(&request)
	if err != nil {
		logger.Errorf("error while uploading artifact: %s", err)
	}
	logger.Infof("Artifact %s:%s uploaded successfully, response: %s", artifactId, artifactVersion, string(*response))
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

func unzipSource(artifactId, artifactVersion, packageId string) error {
	source := fmt.Sprintf("%s:%s.zip", artifactId, artifactVersion) // zip artifact file path
	zipReader, err := zip.OpenReader(source)
	if err != nil {
		return fmt.Errorf("failed to open zip file %s: %w", source, err)
	}

	// Write files to the current directory
	for _, zippedFile := range zipReader.File {
		artifactBaseDir := fmt.Sprintf("%s/%s/%s", CPI_BASE_REPO, packageId, artifactId)
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

func init() {
	if _, err := os.Stat("./mmt-cpi-packages/"); os.IsNotExist(err) {
		githubRepo, err = git.PlainClone("./mmt-cpi-packages/", false, &git.CloneOptions{
			URL:      "https://github.wdf.sap.corp/MaCo-MMT/mmt-cpi-packages",
			Progress: os.Stdout,
			Auth:     githubAuth,
		})
		if err != nil {
			logger.Errorf("error when cloning git repo: %v", err)
			return
		}
	} else {
		githubRepo, err = git.PlainOpen("./mmt-cpi-packages/")
		if err != nil {
			logger.Errorf("error when opening git repo: %v", err)
			return
		}
	}
}

func CommitAndPushChanges(path string, tag string, branch, commitMessage string) error {

	worktree, err := githubRepo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// checkout to cpi instance corresponding branch
	branchRef := plumbing.NewBranchReferenceName(branch)
	_, err = githubRepo.Reference(branchRef, true)
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
	if err == nil {
		if err := worktree.Checkout(&git.CheckoutOptions{
			Branch: branchRef,
			Keep:   true,
		}); err != nil {
			return fmt.Errorf("failed to checkout branch %s: %w", branch, err)
		}
	}

	// Add all changes
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
			Name:  "Doug Liu",
			Email: "doug.liu@sap.com",
			When:  time.Now(),
		},
	}); err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}

	if _, err = githubRepo.CreateTag(tag, commitId, &git.CreateTagOptions{
		Message: "tag is :" + tag,
	}); err != nil {
		return fmt.Errorf("failed to create tag %s: %w", tag, err)
	}

	// Push the changes
	if err := githubRepo.Push(&git.PushOptions{
		Auth:       githubAuth,
		FollowTags: true,
	}); err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}

	return nil
}
