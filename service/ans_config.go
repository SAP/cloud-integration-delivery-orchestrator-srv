package service

import (
	"context"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/notify"
)

// AnsStatus represents the ANS service binding status for display in System Config.
type AnsStatus struct {
	Bound    bool   `json:"bound"`
	Endpoint string `json:"endpoint,omitempty"`
}

// GetAnsStatus checks whether the ANS service binding exists in VCAP_SERVICES.
func (s *Service) GetAnsStatus() AnsStatus {
	creds := env.AnsCredential()
	if creds == nil {
		return AnsStatus{Bound: false}
	}
	return AnsStatus{Bound: true, Endpoint: creds.ApiUrl}
}

// TestAnsConnection verifies that the ANS OAuth2 credentials are valid.
func (s *Service) TestAnsConnection(ctx context.Context) ConnectivityStatus {
	client := notify.NewAnsClient()
	if client == nil {
		return ConnectivityStatus{Name: "ANS", Type: "ans", Status: "error", Message: "alert-notification service not bound"}
	}
	if err := client.TestConnection(ctx); err != nil {
		return ConnectivityStatus{Name: "ANS", Type: "ans", Status: "error", Message: err.Error()}
	}
	return ConnectivityStatus{Name: "ANS", Type: "ans", Status: "ok", Message: "authenticated successfully"}
}
