package service

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/consts"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/cpi"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
)

type PackageArtifact struct {
	TechID      string
	Version     string
	PackageID   string
	Name        string
	Description string
	CreatedBy   string
	CreatedAt   string
	ModifiedBy  string
	ModifiedAt  string
	Type        string
}

// BatchPackageResult holds the result of fetching artifacts for one package.
type BatchPackageResult struct {
	PackageID string            `json:"packageId"`
	Artifacts []PackageArtifact `json:"artifacts"`
	Error     string            `json:"error,omitempty"`
}

// GetPackageArtifactsBatch fetches artifacts for multiple packages concurrently.
// Concurrency is bounded by the CPI client's built-in semaphore.
// Failed packages are reported in the result (partial success) rather than aborting the whole operation.
func GetPackageArtifactsBatch(ctx context.Context, client IntegrationService, packageIDs []string) []BatchPackageResult {
	results := make([]BatchPackageResult, len(packageIDs))
	var wg sync.WaitGroup

	for i, pkgID := range packageIDs {
		wg.Add(1)
		go func(idx int, pid string) {
			defer wg.Done()
			arts, err := GetPackageArtifacts(ctx, client, pid)
			if err != nil {
				results[idx] = BatchPackageResult{PackageID: pid, Error: err.Error()}
			} else {
				results[idx] = BatchPackageResult{PackageID: pid, Artifacts: arts}
			}
		}(i, pkgID)
	}
	wg.Wait()
	return results
}

// isNotFoundError checks if the error is a 404 HTTP response.
func isNotFoundError(err error) bool {
	var httpErr *env.HttpResponseError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
		return true
	}
	return false
}

// GetPackageArtifacts returns all design-time artifacts of all supported types for a package,
// fetching all types in parallel. Types that return 404 (package doesn't contain that type)
// are silently skipped.
func GetPackageArtifacts(ctx context.Context, client IntegrationService, packageID string) ([]PackageArtifact, error) {
	types := consts.AllArtifactTypes()

	type result struct {
		items []cpi.ArtifactCommonItem
		aType consts.ArtifactType
		err   error
	}
	results := make([]result, len(types))
	var wg sync.WaitGroup

	for i, at := range types {
		wg.Add(1)
		go func(idx int, artifactType consts.ArtifactType) {
			defer wg.Done()
			items, err := client.GetPackageArtifactsByType(ctx, packageID, artifactType)
			results[idx] = result{items: items, aType: artifactType, err: err}
		}(i, at)
	}
	wg.Wait()

	var artifacts []PackageArtifact
	for _, r := range results {
		if r.err != nil {
			if isNotFoundError(r.err) {
				continue // package doesn't contain this artifact type — skip
			}
			return nil, r.err
		}
		for _, item := range r.items {
			artifacts = append(artifacts, PackageArtifact{
				TechID:      item.ID,
				Version:     item.Version,
				PackageID:   item.PackageID,
				Name:        item.Name,
				Description: item.Description,
				CreatedBy:   item.CreatedBy,
				CreatedAt:   item.CreatedAt,
				ModifiedBy:  item.ModifiedBy,
				ModifiedAt:  item.ModifiedAt,
				Type:        string(r.aType),
			})
		}
	}
	return artifacts, nil
}
