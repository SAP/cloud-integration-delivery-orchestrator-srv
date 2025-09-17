package test

import (
	"context"
	"testing"
	"mmt-delivery/pkg/tms"
)

func TestTms(t *testing.T) {
	ctx := context.Background()
	tmsClient, err1 := tms.NewClient(ctx)
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
