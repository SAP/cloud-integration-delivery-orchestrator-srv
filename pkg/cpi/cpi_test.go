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
	t.Logf("the packages in the cpi tenant: %#v", packages)

	for _, packageItem := range packages.D.Results {
		packageID := packageItem.ID
		packageItemInfo, error3 := client.GetPackage(packageID)
		if error3 != nil {
			t.Fatalf("error when getting packages from cpi, %v\n  ", error2)
		}
		t.Logf("the packages in the cpi tenant: %#v", packageItemInfo)
	}
}
