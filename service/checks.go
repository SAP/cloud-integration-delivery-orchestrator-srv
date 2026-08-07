package service

import (
	"context"
	"errors"
	"fmt"
	"mmt-delivery/db"
	"mmt-delivery/pkg/env"
	"net/http"
	"strconv"
	"strings"

	"github.com/gobwas/glob"
)

func (s *Service) DeliveryRuleCheck(ctx context.Context, op *db.ArtifactTenantOperation, rule *db.DeliveryRule) error {
	// First priority: reject draft (Active) versions
	if err := checkDraftVersion(op); err != nil {
		return err
	}
	// Second: artifact version matches pattern in delivery rule
	if err := checkVersionPattern(op, rule); err != nil {
		return err
	}
	// Third: version downgrade check per target tenant
	for _, tenant := range rule.IncludedTenants {
		if op.TenantID == tenant.ID {
			continue
		}
		if err := s.checkVersionDowngradeInTenant(ctx, op, &tenant); err != nil {
			return err
		}
	}
	return nil
}

// isDraftVersion returns true if the version string represents a draft/Active state.
func isDraftVersion(version string) bool {
	return strings.EqualFold(version, "active")
}

// checkDraftVersion rejects operations with draft (Active) versions.
// Draft artifacts cannot be delivered — only formally versioned artifacts are allowed.
func checkDraftVersion(op *db.ArtifactTenantOperation) error {
	if isDraftVersion(op.ArtifactVersion) {
		return fmt.Errorf("artifact %s has draft version (Active), only formally versioned artifacts can be delivered", op.ArtifactTechID)
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

func (s *Service) checkVersionDowngradeInTenant(ctx context.Context, op *db.ArtifactTenantOperation, targetTenant *db.CpiTenant) error {
	cli, err := s.CPI(ctx, targetTenant.PirApiDestinationName)
	if err != nil {
		return err
	}
	// Use generic Direct API to get the target's current version
	art, err := cli.GetDesignTimeArtifact(ctx, op.ArtifactTechID, "active", op.ArtifactType)
	if err != nil {
		var httpErr *env.HttpResponseError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil // artifact does not exist in target tenant yet — no downgrade risk
		}
		return err
	}
	targetVersion := art.Version
	if compareCPIVersion(op.ArtifactVersion, targetVersion) < 0 {
		return fmt.Errorf("artifact %s: delivering version %s to tenant %s would downgrade existing version %s, please confirm",
			op.ArtifactTechID, op.ArtifactVersion, targetTenant.Name, targetVersion)
	}
	return nil
}

// cpiVersion represents a parsed CPI artifact version: major.minor.micro[.qualifier]
// Format defined by OSGi Bundle-Version spec, enforced by CPI UI.
type cpiVersion struct {
	Major     int
	Minor     int
	Micro     int
	Qualifier string
}

// parseCPIVersion parses a version string in the format "major.minor.micro[.qualifier]".
// If parsing fails (non-numeric segments), it returns a zero version with the raw string as qualifier
// to ensure graceful degradation to string comparison.
func parseCPIVersion(v string) cpiVersion {
	parts := strings.SplitN(v, ".", 4)

	var cv cpiVersion
	if len(parts) >= 1 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			cv.Major = n
		} else {
			return cpiVersion{Qualifier: v}
		}
	}
	if len(parts) >= 2 {
		if n, err := strconv.Atoi(parts[1]); err == nil {
			cv.Minor = n
		} else {
			return cpiVersion{Qualifier: v}
		}
	}
	if len(parts) >= 3 {
		if n, err := strconv.Atoi(parts[2]); err == nil {
			cv.Micro = n
		} else {
			return cpiVersion{Qualifier: v}
		}
	}
	if len(parts) >= 4 {
		cv.Qualifier = parts[3]
	}
	return cv
}

// compareCPIVersion compares two CPI artifact version strings.
// Returns -1 if a < b, 0 if a == b, +1 if a > b.
// Comparison order: Major (int) → Minor (int) → Micro (int) → Qualifier.
// Qualifier rules:
//   - Both pure numeric: compare as integers (e.g., 3 > 2)
//   - Otherwise: treat as equal (non-numeric qualifiers have no deterministic ordering)
func compareCPIVersion(a, b string) int {
	va := parseCPIVersion(a)
	vb := parseCPIVersion(b)

	if va.Major != vb.Major {
		return intCmp(va.Major, vb.Major)
	}
	if va.Minor != vb.Minor {
		return intCmp(va.Minor, vb.Minor)
	}
	if va.Micro != vb.Micro {
		return intCmp(va.Micro, vb.Micro)
	}
	return compareQualifier(va.Qualifier, vb.Qualifier)
}

// compareQualifier compares two qualifier strings.
// If both are pure numeric, compare as integers; otherwise treat as equal.
func compareQualifier(a, b string) int {
	if a == b {
		return 0
	}
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return intCmp(na, nb)
	}
	return 0
}

func intCmp(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// check tr Number existence in source tenant, and check state is RELEASED
func (s *Service) TrExist(ctx context.Context, op *db.ArtifactTenantOperation, sourceTenant *db.CpiTenant) (bool, error) {
	trNumber := op.TransportRequestNumber
	if trNumber == "" {
		return false, fmt.Errorf("artifact %s has empty transport request number", op.ArtifactTechID)
	}
	tmsClient, err := s.TmsSvc(ctx)
	if err != nil {
		return false, fmt.Errorf("error resolving TMS client: %w", err)
	}
	trV1, err := tmsClient.GetTransportRequest(ctx, trNumber) // v1 to check state
	if err != nil {
		return false, fmt.Errorf("error when getting transport request %s, the tr number may not exist, error message: %s", trNumber, err)
	}
	if trV1 == nil || trV1.ID == 0 || trV1.State != "RELEASED" { // only released tr can be imported
		return false, fmt.Errorf("artifact %s has invalid transport request number %s", op.ArtifactTechID, trNumber)
	}
	if trV1.Origin != sourceTenant.TmsSourceNodeName { // check if match source tenant. can only be checked by origin node name, not id.
		return false, fmt.Errorf("artifact %s has transport request number %s not from source tenant node %s", op.ArtifactTechID, trNumber, sourceTenant.TmsSourceNodeName)
	}
	// check Content Field, should match techID, Version, Type
	index := -1
	for i, md := range trV1.Content[0].Metadata {
		// NOTE: tms response use Name, not tech ID
		if (md.Name == op.ArtifactName || md.Name == op.ArtifactTechID) && md.Type == op.ArtifactType && md.Version == op.ArtifactVersion {
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

func (s *Service) BatchTrExist(ctx context.Context, ops []db.ArtifactTenantOperation, sourceTenant *db.CpiTenant) (bool, error) {
	errOps := make(map[uint]error)
	for i := range ops {
		op := &ops[i]
		_, err := s.TrExist(ctx, op, sourceTenant)
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
