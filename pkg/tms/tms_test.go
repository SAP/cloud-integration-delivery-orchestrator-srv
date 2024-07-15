package tms

import (
	"context"
	"os"
	"testing"
)

func TestTms(t *testing.T) {
	tms_auth_url, _ := os.LookupEnv("TMS_AUTH_URL")
	tms_v1_api, _ := os.LookupEnv("TMS_V1_API")
	tms_v2_api, _ := os.LookupEnv("TMS_V2_API")
	tms_auth_client_id, _ := os.LookupEnv("TMS_AUTH_CLIENT_ID")
	tms_auth_client_secret, _ := os.LookupEnv("TMS_AUTH_CLIENT_SECRET")
	ctx := context.Background()
	tmsClient, err1 := NewTMSClient(ctx, tms_auth_client_id, tms_auth_client_secret, tms_auth_url, tms_v2_api, tms_v1_api)
	if err1 != nil {
		t.Fatalf("Error when creating tms client, error message %s", err1)
	}
	nodes, err2 := tmsClient.GetNodes()
	if err2 != nil {
		t.Fatalf("Error when getting nodes, error message %s", err2)
	}

	for _, node := range nodes {
		id := node.ID
		nodeInfo, err3 := tmsClient.GetNode(id)
		if err3 != nil {
			t.Fatalf("Error when getting node info, error message %s", err3)
		}
		nodeName := nodeInfo.Name
		t.Logf("the node info %s\n", nodeName)
		nodeID := tmsClient.GetNodeID(nodeName)
		t.Logf("nodeID %d", nodeID)
	}
}
