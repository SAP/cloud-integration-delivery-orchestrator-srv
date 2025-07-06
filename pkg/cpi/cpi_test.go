package cpi

import (
	"context"
	"testing"
)

func TestSyncArtifactToGit(t *testing.T) {
	ctx := context.Background()
	client, err := NewClient(ctx, "DEST_CPIAPI_DEV")
	if err != nil {
		t.Fatalf("error when authenticating, %v\n  ", err)
	}

	// Replace with actual artifact ID and version
	artifactID := "Test_Iflow_Delivery"
	artifactVersion := "1.0.1"

	err = client.SyncToGithub(artifactID, artifactVersion, "IntegrationFlow", "SAPMaCoforUtilitiesMonitor", "cpi-mmt-dev", "testuser", "2023-10-01T12:00:00Z", "commnet")
	if err != nil {
		t.Fatalf("error when downloading artifact: %v\n  ", err)
	}
}

func TestUploadArtifact(t *testing.T) {
	ctx := context.Background()
	client, err := NewClient(ctx, "DEST_CPIAPI_DEV")
	if err != nil {
		t.Fatalf("error when authenticating, %v\n  ", err)
	}

	// Replace with actual artifact ID and version
	artifactID := "Test_Iflow_Delivery"
	artifactName := "Demo Artifact for mmt-devops CPI delivery"
	artifactVersion := "1.0.1"
	packageId := "SAPMaCoforUtilitiesMonitor"

	client.UploadArtifact(artifactID, artifactName, artifactVersion, packageId)

	t.Logf("Artifact %s version %s uploaded successfully\n", artifactID, artifactVersion)
}
func TestCPIPackages(t *testing.T) {
	ctx := context.Background()
	client, err1 := NewClient(ctx, "")
	if err1 != nil {
		t.Fatalf("error when authenticating, %v\n  ", err1)
	}

	packages, error2 := client.GetPackages()
	if error2 != nil {
		t.Fatalf("error when getting packages from cpi, %v\n  ", error2)
	}
	//t.Logf("the packages in the cpi tenant: %#v\n", packages)

	for _, packageItem := range packages {
		packageID := packageItem.ID
		packageItemInfo, error3 := client.GetPackage(packageID)
		if error3 != nil {
			t.Fatalf("error when getting packages from cpi, %v\n  ", error3)
		}
		//t.Logf("the packages in the cpi tenant: %#v\n", packageItemInfo)
		iflows, err4 := client.GetPackageIflows(packageItemInfo.ID)
		if err4 != nil {
			t.Fatalf("error when getting iflow in package %s from cpi, %v\n  ", packageItemInfo.Name, err4)
		}

		for _, iflow := range iflows {

			iflowItemInfo, err5 := client.GetPackageIflow(packageItemInfo.ID, iflow.ID, iflow.Version)
			if err5 != nil {
				t.Fatalf("error when getting iflow %s info, %v\n  ", packageItemInfo.Name, err5)
			}
			t.Logf("iflow info %#v\n", iflowItemInfo)
		}

		scriptCollections, err6 := client.GetPackageScriptcollections(packageItemInfo.ID)
		if err6 != nil {
			t.Fatalf("error when getting all script collections in package %s info, %v\n  ", packageItemInfo.Name, err6)
		}

		for _, scriptCollection := range scriptCollections {
			scriptCollectionInfo, err7 := client.GetScriptCollection(scriptCollection.ID, scriptCollection.PackageID)
			if err7 != nil {
				t.Fatalf("error when getting script collections %s info, %v\n  ", scriptCollectionInfo.ID, err7)
			}
			t.Logf("script collection info %#v\n", scriptCollectionInfo)
		}
	}
}
