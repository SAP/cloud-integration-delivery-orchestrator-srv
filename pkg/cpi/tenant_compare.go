// cross tenant version compare

package cpi

type ArtifactVersion struct {
	ArtifactID string
}

func (c *CpiClient) compareTenantVersions(tenant1, tenant2 string) (bool, error) {
	return false, nil
}
