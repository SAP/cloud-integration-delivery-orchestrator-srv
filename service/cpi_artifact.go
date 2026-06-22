package service

import (
	"context"
	"sync"

	"mmt-delivery/consts"
	"mmt-delivery/pkg/cpi"
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

// GetPackageArtifacts returns all design-time artifacts (IFlows + ScriptCollections) for a package,
// fetching both types in parallel.
func GetPackageArtifacts(ctx context.Context, client IntegrationService, packageID string) ([]PackageArtifact, error) {
	var (
		iflows   []cpi.IflowItem
		scs      []cpi.ScriptCollectionItem
		iflowErr error
		scErr    error
		wg       sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		iflows, iflowErr = client.GetPackageIflows(ctx, packageID)
	}()
	go func() {
		defer wg.Done()
		scs, scErr = client.GetPackageScriptcollections(ctx, packageID)
	}()
	wg.Wait()

	if iflowErr != nil {
		return nil, iflowErr
	}
	if scErr != nil {
		return nil, scErr
	}

	artifacts := make([]PackageArtifact, 0, len(iflows)+len(scs))
	for _, f := range iflows {
		artifacts = append(artifacts, PackageArtifact{
			TechID: f.ID, Version: f.Version, PackageID: f.PackageID,
			Name: f.Name, Description: f.Description,
			CreatedBy: f.CreatedBy, CreatedAt: f.CreatedAt,
			ModifiedBy: f.ModifiedBy, ModifiedAt: f.ModifiedAt,
			Type: string(consts.Artifact_Type_Iflow),
		})
	}
	for _, sc := range scs {
		artifacts = append(artifacts, PackageArtifact{
			TechID: sc.ID, Version: sc.Version, PackageID: sc.PackageID,
			Name: sc.Name, Description: sc.Description,
			CreatedBy: sc.CreatedBy, CreatedAt: sc.CreatedAt,
			ModifiedBy: sc.ModifiedBy, ModifiedAt: sc.ModifiedAt,
			Type: string(consts.Artifact_Type_Sc),
		})
	}
	return artifacts, nil
}
