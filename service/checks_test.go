package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mmt-delivery/consts"
	"mmt-delivery/db"
	"mmt-delivery/pkg/tms"

	"gorm.io/gorm"
)

func TestTrExist_RejectsEmptyTransportRequestNumber(t *testing.T) {
	svc := newTestService(nil)

	ok, err := svc.TrExist(&db.ArtifactTenantOperation{
		ArtifactTechID: "artifact-1",
	}, &db.CpiTenant{TmsSourceNodeName: "SRC"})

	if ok {
		t.Fatal("expected false for empty transport request number")
	}
	if err == nil || !strings.Contains(err.Error(), "empty transport request number") {
		t.Fatalf("expected empty transport request number error, got %v", err)
	}
}

func TestTrExist_ValidatesTransportRequestStateOriginAndMetadata(t *testing.T) {
	source := &db.CpiTenant{TmsSourceNodeName: "SRC-NODE"}
	baseOp := db.ArtifactTenantOperation{
		ArtifactTechID:         "artifact-tech",
		ArtifactName:           "Artifact Name",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "TR-1000",
	}

	cases := []struct {
		name    string
		tr      *tms.TransportRequestV1
		wantErr string
	}{
		{
			name: "non released transport request rejected",
			tr: func() *tms.TransportRequestV1 {
				tr := validTR("TR-1000", source.TmsSourceNodeName, baseOp.ArtifactTechID, baseOp.ArtifactVersion, baseOp.ArtifactType)
				tr.State = "ARCHIVED"
				return tr
			}(),
			wantErr: "invalid transport request number",
		},
		{
			name: "wrong origin rejected",
			tr: func() *tms.TransportRequestV1 {
				tr := validTR("TR-1000", "OTHER-NODE", baseOp.ArtifactTechID, baseOp.ArtifactVersion, baseOp.ArtifactType)
				return tr
			}(),
			wantErr: "not from source tenant node",
		},
		{
			name: "metadata mismatch rejected",
			tr: &tms.TransportRequestV1{
				ID:     1,
				State:  "RELEASED",
				Origin: source.TmsSourceNodeName,
				Content: []tms.Content{{
					Metadata: []tms.Metadata{{
						Name:    "other-artifact",
						Type:    consts.Artifact_Type_Iflow,
						Version: "9.9.9",
					}},
				}},
			},
			wantErr: "not match. May use a wrong trNumber",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(nil, testServiceOpts{
				tms: &mockTMSClient{
					transportRequests: map[string]*tms.TransportRequestV1{
						baseOp.TransportRequestNumber: tc.tr,
					},
				},
			})

			ok, err := svc.TrExist(&baseOp, source)
			if ok {
				t.Fatal("expected invalid transport request to return false")
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestTrExist_AcceptsArtifactNameOrTechIDMetadata(t *testing.T) {
	source := &db.CpiTenant{TmsSourceNodeName: "SRC-NODE"}
	op := db.ArtifactTenantOperation{
		ArtifactTechID:         "artifact-tech",
		ArtifactName:           "Artifact Name",
		ArtifactVersion:        "1.0.0",
		ArtifactType:           consts.Artifact_Type_Iflow,
		TransportRequestNumber: "TR-2000",
	}

	cases := []struct {
		name         string
		metadataName string
	}{
		{name: "artifact name matches", metadataName: op.ArtifactName},
		{name: "artifact tech id matches", metadataName: op.ArtifactTechID},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(nil, testServiceOpts{
				tms: &mockTMSClient{
					transportRequests: map[string]*tms.TransportRequestV1{
						op.TransportRequestNumber: {
							ID:     1,
							State:  "RELEASED",
							Origin: source.TmsSourceNodeName,
							Content: []tms.Content{{
								Metadata: []tms.Metadata{{
									Name:    tc.metadataName,
									Type:    op.ArtifactType,
									Version: op.ArtifactVersion,
								}},
							}},
						},
					},
				},
			})

			ok, err := svc.TrExist(&op, source)
			if err != nil {
				t.Fatalf("expected valid transport request, got %v", err)
			}
			if !ok {
				t.Fatal("expected valid transport request to return true")
			}
		})
	}
}

func TestBatchTrExist_AggregatesFailures(t *testing.T) {
	source := &db.CpiTenant{TmsSourceNodeName: "SRC-NODE"}
	svc := newTestService(nil, testServiceOpts{
		tms: &mockTMSClient{
			transportRequests: map[string]*tms.TransportRequestV1{
				"TR-OK": validTR("TR-OK", source.TmsSourceNodeName, "artifact-ok", "1.0.0", consts.Artifact_Type_Iflow),
				"TR-BAD": {
					ID:     1,
					State:  "RELEASED",
					Origin: "OTHER-NODE",
					Content: []tms.Content{{
						Metadata: []tms.Metadata{{
							Name:    "artifact-bad",
							Type:    consts.Artifact_Type_Iflow,
							Version: "1.0.0",
						}},
					}},
				},
			},
		},
	})

	ops := []db.ArtifactTenantOperation{
		{Model: gorm.Model{ID: 1}, ArtifactTechID: "artifact-ok", ArtifactName: "artifact-ok", ArtifactVersion: "1.0.0", ArtifactType: consts.Artifact_Type_Iflow, TransportRequestNumber: "TR-OK"},
		{Model: gorm.Model{ID: 2}, ArtifactTechID: "artifact-bad", ArtifactName: "artifact-bad", ArtifactVersion: "1.0.0", ArtifactType: consts.Artifact_Type_Iflow, TransportRequestNumber: "TR-BAD"},
		{Model: gorm.Model{ID: 3}, ArtifactTechID: "artifact-missing"},
	}

	ok, err := svc.BatchTrExist(ops, source)
	if ok {
		t.Fatal("expected batch check to fail")
	}
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	for _, want := range []string{"operation 2", "operation 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected aggregated error to contain %q, got %v", want, err)
		}
	}
}

func TestDeliveryRuleCheck_RejectsPatternMismatchWithoutCallingCPI(t *testing.T) {
	callCount := 0
	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		callCount++
		return &mockCPIClientWithDesignTime{}, nil
	})

	err := svc.DeliveryRuleCheck(&db.ArtifactTenantOperation{
		ArtifactTechID:  "artifact-1",
		ArtifactVersion: "2.0.0",
		ArtifactType:    consts.Artifact_Type_Iflow,
		TenantID:        1,
	}, &db.DeliveryRule{
		Name:           "rule-1",
		VersionPattern: "1.*",
	})

	if err == nil || !strings.Contains(err.Error(), "not match pattern") {
		t.Fatalf("expected version pattern mismatch, got %v", err)
	}
	if callCount != 0 {
		t.Fatalf("expected CPI lookup to be skipped, got %d calls", callCount)
	}
}

func TestDeliveryRuleCheck_SkipsCurrentTenantInIncludedTenants(t *testing.T) {
	callCount := 0
	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		callCount++
		return nil, errors.New("should not be called")
	})

	sourceTenant := db.CpiTenant{Model: gorm.Model{ID: 10}, Name: "source"}
	err := svc.DeliveryRuleCheck(&db.ArtifactTenantOperation{
		ArtifactTechID:  "artifact-1",
		ArtifactVersion: "1.0.0",
		ArtifactType:    consts.Artifact_Type_Iflow,
		TenantID:        sourceTenant.ID,
	}, &db.DeliveryRule{
		Name:           "rule-1",
		VersionPattern: "1.*",
		IncludedTenants: []db.CpiTenant{
			sourceTenant,
		},
	})

	if err != nil {
		t.Fatalf("expected current tenant to be skipped, got %v", err)
	}
	if callCount != 0 {
		t.Fatalf("expected no CPI lookups for current tenant, got %d", callCount)
	}
}

func TestDeliveryRuleCheck_RejectsDowngradeInTargetTenant(t *testing.T) {
	target := db.CpiTenant{
		Model:                 gorm.Model{ID: 20},
		Name:                  "target-tenant",
		PirApiDestinationName: "pir-target",
	}

	svc := newTestService(func(ctx context.Context, tenant string) (IntegrationService, error) {
		return &mockCPIClientWithDesignTime{
			iflowVersions: map[string]string{
				"artifact-1": "1.2.0",
			},
		}, nil
	})

	err := svc.DeliveryRuleCheck(&db.ArtifactTenantOperation{
		ArtifactTechID:  "artifact-1",
		ArtifactVersion: "1.0.0",
		ArtifactType:    consts.Artifact_Type_Iflow,
		TenantID:        10,
	}, &db.DeliveryRule{
		Name:           "rule-1",
		VersionPattern: "1.*",
		IncludedTenants: []db.CpiTenant{
			target,
		},
	})

	if err == nil || !strings.Contains(err.Error(), "would downgrade existing version") {
		t.Fatalf("expected downgrade error, got %v", err)
	}
	if !strings.Contains(err.Error(), target.Name) {
		t.Fatalf("expected error to mention target tenant %q, got %v", target.Name, err)
	}
}
