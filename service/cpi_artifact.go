package service

import (
	"context"
	"sync"

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

const (
	ArtifactTypeIFlow            = "Integration Flow"
	ArtifactTypeScriptCollection = "Script Collection"
)

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
			Type: ArtifactTypeIFlow,
		})
	}
	for _, sc := range scs {
		artifacts = append(artifacts, PackageArtifact{
			TechID: sc.ID, Version: sc.Version, PackageID: sc.PackageID,
			Name: sc.Name, Description: sc.Description,
			CreatedBy: sc.CreatedBy, CreatedAt: sc.CreatedAt,
			ModifiedBy: sc.ModifiedBy, ModifiedAt: sc.ModifiedAt,
			Type: ArtifactTypeScriptCollection,
		})
	}
	return artifacts, nil
}
