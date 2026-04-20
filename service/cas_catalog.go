package service

import (
	"context"
	"fmt"
)

// CasPackage is one Cloud Integration package as returned by ListCasPackages.
// Fields map directly from CatalogContentResource; no DB enrichment needed.
type CasPackage struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Version   string        `json:"version"`
	Artifacts []CasArtifact `json:"artifacts"`
}

// CasArtifact is one artifact within a CasPackage.
// techID = CatalogComponent.Name = Artifact.TechID in our DB.
// guid   = CatalogComponent.ID   = artifact GUID required by generateTR.
type CasArtifact struct {
	TechID     string `json:"techID"`
	GUID       string `json:"guid"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Version    string `json:"version"`
	Exportable bool   `json:"exportable"`
}

// ListCasPackages fetches the full package+artifact catalog for tenantID from CAS.
// Only entries with subType="package" are returned; "Destination" entries are ignored.
// Requires tenant.CasEngineDestinationName to be configured.
func (s *Service) ListCasPackages(ctx context.Context, tenantID uint) ([]CasPackage, error) {
	casClient, err := s.CAS(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("ListCasPackages: %w", err)
	}

	catalog, err := casClient.ListContentResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("ListCasPackages: fetch catalog: %w", err)
	}

	var packages []CasPackage
	for _, entry := range catalog {
		if entry.SubType != "package" {
			continue
		}
		artifacts := make([]CasArtifact, 0, len(entry.Components))
		for _, comp := range entry.Components {
			artifacts = append(artifacts, CasArtifact{
				TechID:     comp.Name,
				GUID:       comp.ID,
				Name:       comp.Name,
				Type:       comp.Type,
				Version:    comp.Version,
				Exportable: comp.Exportable,
			})
		}
		packages = append(packages, CasPackage{
			ID:        entry.ID,
			Name:      entry.Name,
			Version:   entry.Version,
			Artifacts: artifacts,
		})
	}
	return packages, nil
}
