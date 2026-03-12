package service

import (
	"context"
	"errors"
	"fmt"
	"mmt-delivery/consts"
	"mmt-delivery/db"
	"strings"

	"github.com/gobwas/glob"
	"golang.org/x/mod/semver"
)

func (s *Service) DeliveryRuleCheck(op *db.ArtifactTenantOperation, rule *db.DeliveryRule) error {
	// artifact version matches pattern in delivery rule
	if err := checkVersionPattern(op, rule); err != nil {
		return err
	}
	for _, tenant := range rule.IncludedTenants {
		if op.TenantID == tenant.ID {
			continue
		}
		// before deliver, should check if version would cause downgrade in target tenants
		if err := s.checkVersionDowngradeInTenant(op, &tenant); err != nil {
			return err
		}
	}
	return nil
}

// matchVersionPattern checks if a version string matches a glob pattern.
// Returns true if pattern is empty or if the version matches.
//
// Examples: 5.2.2 matches 5.2.* → true; 6.1.3 matches 6.2.* → false
func matchVersionPattern(version, pattern string) bool {
	if pattern == "" {
		return true
	}
	g := glob.MustCompile(pattern)
	return g.Match(version)
}

// checkVersionPattern validates that an artifact's version matches the delivery rule's pattern.
// Used in InsertTenantOps / DeliveryRuleCheck for per-op validation.
func checkVersionPattern(op *db.ArtifactTenantOperation, rule *db.DeliveryRule) error {
	if !matchVersionPattern(op.ArtifactVersion, rule.VersionPattern) {
		return fmt.Errorf("artifact %s has version %s not match pattern %s(delivery rule: %s)", op.ArtifactTechID, op.ArtifactVersion, rule.VersionPattern, rule.Name)
	}
	return nil
}

func (s *Service) checkVersionDowngradeInTenant(op *db.ArtifactTenantOperation, targetTenant *db.CpiTenant) error {
	cli, err := s.CPI(context.Background(), targetTenant.CpiEndpoint.Name)
	if err != nil {
		return err
	}
	sourceVersion := op.ArtifactVersion
	var targetVersion string
	switch op.Artifact.Type {
	case consts.Artifact_Type_Iflow:
		iflow, err := cli.GetDesignTimeIflow(context.Background(), op.ArtifactTechID, "active")
		if err != nil {
			return err
		}
		targetVersion = iflow.Version
	case consts.Artifact_Type_Sc:
		sc, err := cli.GetDesignTimeScriptCollection(context.Background(), op.ArtifactTechID, "active")
		if err != nil {
			return err
		}
		targetVersion = sc.Version
	}
	if !strings.HasPrefix(targetVersion, "v") {
		targetVersion = "v" + targetVersion
	}
	if !strings.HasPrefix(sourceVersion, "v") {
		sourceVersion = "v" + sourceVersion
	}
	if semver.Compare(sourceVersion, targetVersion) < 0 {
		return fmt.Errorf("artifact %s: delivering version %s to tenant %s would downgrade existing version %s, please confirm",
			op.ArtifactTechID, sourceVersion, targetTenant.CpiEndpoint.Name, targetVersion)
	}
	return nil
}

// check tr Number existence in source tenant, and check state is RELEASED
func (s *Service) TrExist(op *db.ArtifactTenantOperation, sourceTenant *db.CpiTenant) (bool, error) {
	trNumber := op.TransportRequestNumber
	if trNumber == "" {
		return false, fmt.Errorf("artifact %s has empty transport request number", op.ArtifactTechID)
	}
	trV1, err := s.TMS.GetTransportRequest(context.Background(), trNumber) // v1 to check state
	if err != nil {
		return false, fmt.Errorf("error when getting transport request %s, the tr number may not exist, error message: %s", trNumber, err)
	}
	if trV1 == nil || trV1.ID == 0 || trV1.State != "RELEASED" { // only released tr can be imported
		return false, fmt.Errorf("artifact %s has invalid transport request number %s", op.ArtifactTechID, trNumber)
	}
	if trV1.Origin != sourceTenant.TransportNodeName { // check if match source tenant. can only be checked by origin node name, not id.
		return false, fmt.Errorf("artifact %s has transport request number %s not from source tenant node %s", op.ArtifactTechID, trNumber, sourceTenant.TransportNodeName)
	}
	// check Content Field, should match techID, Version, Type
	index := -1
	for i, md := range trV1.Content[0].Metadata {
		// NOTE: tms response use Name, not tech ID
		if (md.Name == op.Artifact.Name || md.Name == op.ArtifactTechID) && md.Type == op.Artifact.Type && md.Version == op.ArtifactVersion {
			index = i
			break
		}
	}
	if index == -1 {
		return false, fmt.Errorf("artifact %s, trNumber %s: not match. May use a wrong trNumber for this artifact", op.ArtifactTechID, trNumber)
	}

	// update status of artifact tenant operation
	return true, nil
}

func (s *Service) BatchTrExist(ops []db.ArtifactTenantOperation, sourceTenant *db.CpiTenant) (bool, error) {
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		_, err := s.TrExist(op, sourceTenant)
		if err != nil {
			errOps[op.ID] = err
		}
	}
	if len(errOps) > 0 {
		errMsg := "transport request existence check failed for some artifact tenant operations:\n"
		for id, err := range errOps {
			errMsg += fmt.Sprintf("  operation %d: %s\n", id, err)
		}
		return false, errors.New(errMsg)
	}
	return true, nil
}
