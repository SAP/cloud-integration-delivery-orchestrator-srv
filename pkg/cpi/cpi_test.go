package cpi

import (
	"context"
	"os"
	"testing"
)

func TestCPIPackages(t *testing.T) {
	ctx := context.Background()
	clientID, _ := os.LookupEnv("CPI_AUTH_CLIENT_ID")
	clientSecret, _ := os.LookupEnv("CPI_AUTH_CLIENT_SECRET")
	cpiAuthURL, _ := os.LookupEnv("CPI_AUTH_URL")
	cpiURL, _ := os.LookupEnv("CPI_API_URL")
	client, err1 := NewCPIClient(ctx, clientID, clientSecret, cpiAuthURL, cpiURL)
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
		iflows, err4 := client.GetIflows(packageItemInfo.ID)
		if err4 != nil {
			t.Fatalf("error when getting iflow in package %s from cpi, %v\n  ", packageItemInfo.Name, err4)
		}

		for _, iflow := range iflows {

			iflowItemInfo, err5 := client.GetIflow(packageItemInfo.ID, iflow.ID, iflow.Version)
			if err5 != nil {
				t.Fatalf("error when getting iflow %s info, %v\n  ", packageItemInfo.Name, err5)
			}
			t.Logf("iflow info %#v\n", iflowItemInfo)
		}

		scriptCollections, err6 := client.GetScripts(packageItemInfo.ID)
		if err6 != nil {
			t.Fatalf("error when getting all script collections in package %s info, %v\n  ", packageItemInfo.Name, err6)
		}

		for _, scriptCollection := range scriptCollections {
			scriptCollectionInfo, err7 := client.GetScript(scriptCollection.ID, scriptCollection.PackageID)
			if err7 != nil {
				t.Fatalf("error when getting script collections %s info, %v\n  ", scriptCollectionInfo.ID, err7)
			}
			t.Logf("script collection info %#v\n", scriptCollectionInfo)
		}
	}
}
