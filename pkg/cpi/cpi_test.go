package cpi

import (
	"context"
	"testing"
)

func TestCPIPackages(t *testing.T) {
	ctx := context.Background()
	clientID := "sb-8baf396e-a220-4936-a560-f16c898890b6!b486442|it!b410603"
	clientSecret := "REDACTED"
	cpiAuthURL := "https://cpi-mmt-preprod.authentication.eu10.hana.ondemand.com/oauth/token"
	cpiURL := "https://cpi-mmt-preprod.it-cpi026.cfapps.eu10-002.hana.ondemand.com/api/v1"
	client, err1 := NewCPIClient(ctx, clientID, clientSecret, cpiAuthURL, cpiURL)
	if err1 != nil {
		t.Fatalf("error when authenticating, %v\n  ", err1)
	}

	packages, error2 := client.GetPackages()
	if error2 != nil {
		t.Fatalf("error when getting packages from cpi, %v\n  ", error2)
	}
	//t.Logf("the packages in the cpi tenant: %#v\n", packages)

	for _, packageItem := range packages.D.Results {
		packageID := packageItem.ID
		packageItemInfo, error3 := client.GetPackage(packageID)
		if error3 != nil {
			t.Fatalf("error when getting packages from cpi, %v\n  ", error3)
		}
		//t.Logf("the packages in the cpi tenant: %#v\n", packageItemInfo)
		iflows, err4 := client.GetIflows(packageItemInfo.D.ID)
		if err4 != nil {
			t.Fatalf("error when getting iflow in package %s from cpi, %v\n  ", packageItemInfo.D.Name, err4)
		}

		for _, iflow := range iflows.D.Results {

			iflowItemInfo, err5 := client.GetIflow(packageItemInfo.D.ID, iflow.ID, iflow.Version)
			if err5 != nil {
				t.Fatalf("error when getting iflow %s info, %v\n  ", packageItemInfo.D.Name, err5)
			}
			t.Logf("iflow info %#v\n", iflowItemInfo)
		}

		scriptCollections, err6 := client.GetScripts(packageItemInfo.D.ID)
		if err6 != nil {
			t.Fatalf("error when getting all script collections in package %s info, %v\n  ", packageItemInfo.D.Name, err6)
		}

		for _, scriptCollection := range scriptCollections.D.Results {
			scriptCollectionInfo, err7 := client.GetScript(scriptCollection.ID, scriptCollection.PackageID)
			if err7 != nil {
				t.Fatalf("error when getting script collections %s info, %v\n  ", scriptCollectionInfo.D.ID, err7)
			}
			t.Logf("script collection info %#v\n", scriptCollectionInfo)
		}
	}
}
