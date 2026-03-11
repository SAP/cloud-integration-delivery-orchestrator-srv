package service

import (
	"context"
	"fmt"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/cpi"

	"golang.org/x/sync/errgroup"
)

// FetchPackageArtifacts retrieves all artifact types within a CPI package and returns
// them as a unified []db.Artifact slice. This is the central function for obtaining
// a package's complete design-time artifact list.
//
// Currently fetches Integration Flows and Script Collections in parallel.
// When new artifact types are added (adapter, value mapping, etc.), only this
// function needs to be updated — all downstream consumers benefit automatically.
func FetchPackageArtifacts(ctx context.Context, client IntegrationService, packageID string) ([]db.Artifact, error) {
	var iflows []cpi.IflowItem
	var scriptColls []cpi.ScriptCollectionItem

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		var err error
		iflows, err = client.GetPackageIflows(gctx, packageID)
		if err != nil {
			return fmt.Errorf("GetPackageIflows(%s): %w", packageID, err)
		}
		return nil
	})

	g.Go(func() error {
		var err error
		scriptColls, err = client.GetPackageScriptcollections(gctx, packageID)
		if err != nil {
			return fmt.Errorf("GetPackageScriptcollections(%s): %w", packageID, err)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	artifacts := make([]db.Artifact, 0, len(iflows)+len(scriptColls))
	for _, v := range iflows {
		artifacts = append(artifacts, WrapArtifact(consts.Artifact_Type_Iflow, v))
	}
	for _, v := range scriptColls {
		artifacts = append(artifacts, WrapArtifact(consts.Artifact_Type_Sc, v))
	}
	return artifacts, nil
}

// WrapArtifact normalizes CPI raw types (IflowItem / ScriptCollectionItem) into a
// db.Artifact DTO (not persisted here). Both types embed ArtifactCommonItem.
// Exported so the handler layer can use it if needed.
func WrapArtifact(artifactType consts.ArtifactType, artifact any) db.Artifact {
	switch v := artifact.(type) {
	case cpi.ScriptCollectionItem:
		return db.Artifact{
			TechID:      v.ID,
			Version:     v.Version,
			PackageID:   v.PackageID,
			Name:        v.Name,
			Description: v.Description,
			Type:        artifactType,
			CreatedBy:   v.CreatedBy,
			CreatedAt:   v.CreatedAt,
			ModifiedBy:  v.ModifiedBy,
			ModifiedAt:  v.ModifiedAt,
		}
	case cpi.IflowItem:
		return db.Artifact{
			TechID:      v.ID,
			Version:     v.Version,
			PackageID:   v.PackageID,
			Name:        v.Name,
			Description: v.Description,
			Type:        artifactType,
			CreatedBy:   v.CreatedBy,
			CreatedAt:   v.CreatedAt,
			ModifiedBy:  v.ModifiedBy,
			ModifiedAt:  v.ModifiedAt,
		}
	default:
		return db.Artifact{Type: artifactType}
	}
}
