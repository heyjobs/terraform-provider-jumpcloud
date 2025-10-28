package jumpcloud

import (
	"context"
	"fmt"
	"log"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func dataSourceJumpCloudApplication() *schema.Resource {
	return &schema.Resource{
		Read: dataSourceJumpCloudApplicationRead,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"display_label": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"id": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func dataSourceJumpCloudApplicationRead(d *schema.ResourceData, m interface{}) error {
	log.Printf("[DEBUG] Starting dataSourceJumpCloudApplicationRead")
	configv1 := convertV2toV1Config(m.(*jcapiv2.Configuration))
	client := jcapiv1.NewAPIClient(configv1)
	applicationName, nameExists := d.GetOk("name")
	displayLabel, displayLabelExists := d.GetOk("display_label")

	if !nameExists && !displayLabelExists {
		return fmt.Errorf("either name or display_label must be provided")
	}

	pageSize := int32(100)
	var skip int32 = 0

	for {
		optionalParams := map[string]interface{}{
			"limit": pageSize,
			"skip":  skip,
		}

		// Fetch a page of applications
		appsResponse, _, err := client.ApplicationsApi.ApplicationsList(
			context.Background(),
			"_id, displayName, displayLabel",
			"",
			optionalParams,
		)
		if err != nil {
			return fmt.Errorf("failed to list applications: %w", err)
		}

		results := appsResponse.Results
		log.Printf("[DEBUG] Retrieved %d applications (skip=%d limit=%d)", len(results), skip, pageSize)

		// Check for matching application in this page
		for _, application := range results {
			log.Printf("[DEBUG] Checking application ID=%s DisplayName=%s DisplayLabel=%s",
				application.Id, application.DisplayName, application.DisplayLabel)

			if (nameExists && application.DisplayName == applicationName) ||
				(displayLabelExists && application.DisplayLabel == displayLabel) {
				d.SetId(application.Id)
				return nil
			}
		}
		if len(results) < int(pageSize) {
			break
		}

		skip += pageSize
	}

	return fmt.Errorf("no application found with the provided filters")
}
