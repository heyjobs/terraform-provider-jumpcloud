package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
)

func Test_resourceApplication(t *testing.T) {
	randSuffix := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)
	fullResourceName := "jumpcloud_application.example_app"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck: func() {
		},
		ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			// Create step
			{
				Config: testApplicationConfig(randSuffix, "test_aws_account", "test_attribute_value"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fullResourceName, "display_label", "test_aws_account"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.name", "test_attribute_name"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.value", "test_attribute_value"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.read_only", "false"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.required", "false"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.visible", "true"),
				),
			},
			userImportStep(fullResourceName),
			// Update Step
			{
				Config: testApplicationConfig(randSuffix, "test_aws_account_updated", "updated_test_value"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fullResourceName, "display_label", "test_aws_account_updated"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.name", "test_attribute_name"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.value", "updated_test_value"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.read_only", "false"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.required", "false"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.visible", "true"),
				),
			},
			userImportStep(fullResourceName),
		},
	})
}

// testApplicationConfig generates the Terraform configuration for testing
func testApplicationConfig(randSuffix string, displayLabel string, constantAttrValue string) string {
	return fmt.Sprintf(`
resource "jumpcloud_application" "example_app" {
	display_label        = "%s"
	sso_url              = "https://sso.jumpcloud.com/saml2/example-application_%s"
	saml_role_attribute  = "arn:aws:iam::AWS_ACCOUNT_ID:role/MY_ROLE,arn:aws:iam::AWS_ACCOUNT_ID:saml-provider/MY_SAML_PROVIDER"
	aws_session_duration = 432000

	constant_attributes {
		name  = "test_attribute_name"
		value = "%s"
		read_only = false
		required = false
		visible = true
	}
}
`, displayLabel, randSuffix, constantAttrValue)
}

// userImportStep is used to test resource import functionality
func userImportStep(resourceName string) resource.TestStep {
	return resource.TestStep{
		ResourceName:      resourceName,
		ImportState:       true,
		ImportStateVerify: true,
	}
}
