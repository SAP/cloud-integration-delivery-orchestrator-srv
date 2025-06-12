package cpi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mmt-delivery/env"
	"net/http"
	"os"
	"path/filepath"
)

func (c *CpiClient) downloadArtifact(artifactId string, artifactVersion string) (*[]byte, error) {
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
		return nil, fmt.Errorf("failed to download artifact: %w", err)
	}

	// Unzip the response body
	zipReader, err := zip.NewReader(bytes.NewReader(*artifactContent), int64(len(*artifactContent)))
	if err != nil {
		return nil, fmt.Errorf("failed to read zip file: %w", err)
	}

	// Write files to the current directory
	for _, zippedFile := range zipReader.File {
		artifactBaseDir := fmt.Sprintf("%s:%s", artifactId, artifactVersion)
		targetPath := filepath.Join(artifactBaseDir, zippedFile.Name)

		// Create directories if necessary
		if zippedFile.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, os.ModePerm); err != nil {
				return nil, fmt.Errorf("failed to create directory %s: %w", targetPath, err)
			}
			continue
		}

		// Ensure the parent directory exists
		if err := os.MkdirAll(filepath.Dir(targetPath), os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to create parent directory for %s: %w", targetPath, err)
		}

		// create target file
		targetFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, zippedFile.Mode())
		if err != nil {
			return nil, fmt.Errorf("failed to create file %s: %w", targetPath, err)
		}
		defer targetFile.Close()

		// Write the file content
		rc, err := zippedFile.Open()
		if err != nil {
			return nil, fmt.Errorf("failed to open file %s in zip: %w", zippedFile.Name, err)
		}
		defer rc.Close()

		if _, err := io.Copy(targetFile, rc); err != nil {
			return nil, fmt.Errorf("failed to write file %s: %w", targetPath, err)
		}
	}

	return artifactContent, nil
}
func (c *CpiClient) UploadArtifact(artifactId string, artifactName string, artifactVersion string, packageId string) {
	// fake id just for test
	fakeArtifactId := fmt.Sprintf("FAKE_ID_%s", artifactId)

	artifactDir := filepath.Join(".", fmt.Sprintf("%s:%s", artifactId, artifactVersion))
	zipBuffer, err := zipSource(artifactDir, fmt.Sprintf("%s.zip", fakeArtifactId))

	// Write the zip buffer to disk
	// zipFilePath := filepath.Join(artifactDir, fmt.Sprintf("%s.zip", fakeArtifactId))
	// zipFile, err := os.Create(zipFilePath)
	// if err != nil {
	// 	panic(fmt.Errorf("failed to create zip file %s: %w", zipFilePath, err))
	// }
	// defer zipFile.Close()

	// if _, err := zipBuffer.WriteTo(zipFile); err != nil {
	// 	panic(fmt.Errorf("failed to write zip buffer to file %s: %w", zipFilePath, err))
	// }

	// Encode zipBuffer with base64
	// encodedZipBuffer := base64.StdEncoding.EncodeToString(zipBuffer.Bytes())

	// Create payload
	payload := map[string]any{
		"Name":            artifactName,
		"Id":              fakeArtifactId,
		"PackageId":       packageId,
		"ArtifactContent": zipBuffer.Bytes(),
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
