package kafkas

import (
	"context"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/pkg/errors"
	kasclient "github.com/redhat-developer/app-services-cli/pkg/api/kas/client"
	"io/ioutil"
	"log"
	"redhat.com/rhoas/rhoas-terraform-provider/m/rhoas/cli/connection"
	"redhat.com/rhoas/rhoas-terraform-provider/m/rhoas/utils"
	"time"
)

func ResourceKafka() *schema.Resource {
	return &schema.Resource{
		Description: "`rhoas_kafka` manages a Kafka instance in Red Hat OpenShift Streams for Apache Kafka.",
		CreateContext: kafkaCreate,
		ReadContext:   kafkaRead,
		DeleteContext: kafkaDelete,
		Schema: map[string]*schema.Schema{
			"kafka": &schema.Schema{
				Type:     schema.TypeList,
				MaxItems: 1,
				Required: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"cloud_provider": &schema.Schema{
							Description: "The cloud provider to use. A list of available cloud providers can be obtained using `data.rhoas_cloud_providers`.",
							Type:     schema.TypeString,
							Optional: true,
							Default:  "aws",
							ForceNew: true,
						},
						"multi_az": &schema.Schema{
							Description: "Whether the Kafka instance should be highly available by supporting multi-az",
							Type:     schema.TypeBool,
							Optional: true,
							Default:  true,
							ForceNew: true,
						},
						"region": &schema.Schema{
							Description: "The region to use. A list of available regions can be obtained using `data.rhoas_cloud_providers_regions`.",
							Type:     schema.TypeString,
							Optional: true,
							Default:  "us-east-1",
							ForceNew: true,
						},
						"name": &schema.Schema{
							Description: "The name of the Kafka instance",
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},
						"href": &schema.Schema{
							Type: schema.TypeString,
							Computed: true,
							Description: "The path to the Kafka instance in the REST API",
						},
						"status": &schema.Schema{
							Type: schema.TypeString,
							Computed: true,
							Description: "The status of the Kafka instance",
						},
						"owner": &schema.Schema{
							Type: schema.TypeString,
							Computed: true,
							Description: "The username of the Red Hat account that owns the Kafka instance",
						},
						"bootstrap_server": &schema.Schema{
							Description: "The bootstrap server (host:port)",
							Type: schema.TypeString,
							Computed: true,
						},
						"created_at": &schema.Schema{
							Description: "The RFC3339 date and time at which the Kafka instance was created",
							Type: schema.TypeString,
							Computed: true,
						},
						"updated_at": &schema.Schema{
							Description: "The RFC3339 date and time at which the Kafka instance was last updated",
							Type: schema.TypeString,
							Computed: true,
						},
						"id": &schema.Schema{
							Description: "The unique identifier for the Kafka instance",
							Type: schema.TypeString,
							Computed: true,
						},
						"kind": &schema.Schema{
							Type: schema.TypeString,
							Computed: true,
							Description: "The kind of resource in the API",
						},
						"version": &schema.Schema{
							Description: "The version of Kafka the instance is using",
							Type: schema.TypeString,
							Computed: true,
						},
					},
				},
			},
		},
		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(20 * time.Minute),
		},
	}
}

func kafkaDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	// Warning or errors can be collected in a slice type
	var diags diag.Diagnostics

	c := m.(*connection.KeycloakConnection)

	api := c.API().Kafka()

	apiErr, _, err := api.DeleteKafkaById(ctx, d.Id()).Async(true).Execute()
	if err != nil && err.Error() == "404 " {
		// the resource is deleted already
		d.SetId("")
		return diags
	}
	if err != nil {
		return diag.Errorf("%s%s", err.Error(), apiErr.Reason)
	}

	deleteStateConf := &resource.StateChangeConf{
		Delay: 5 * time.Second,
		Pending: []string{
			"deprovision",
		},
		Refresh: func() (interface{}, string, error) {
			data, resp, err := api.GetKafkaById(ctx, d.Id()).Execute()
			if err != nil && err.Error() == "404 " {
				return data, "404", nil
			}
			if err != nil {
				bodyBytes, ioErr := ioutil.ReadAll(resp.Body)
				if ioErr != nil {
					log.Fatal(ioErr)
				}
				return nil, "", errors.Errorf("%s%s", err.Error(), string(bodyBytes))
			}
			return data, *data.Status, nil
		},
		Target: []string{
			"deleted",
		},
		Timeout:                   d.Timeout(schema.TimeoutCreate),
		MinTimeout:                5 * time.Second,
		NotFoundChecks:            0,
		ContinuousTargetOccurence: 0,
	}

	_, err = deleteStateConf.WaitForStateContext(ctx)
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "Error waiting for example instance (%s) to be created: %s", d.Id()))
	}

	d.SetId("")
	return diags
}

func kafkaRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {

	var diags diag.Diagnostics

	c := m.(*connection.KeycloakConnection)

	api := c.API().Kafka()

	var raw []map[string]interface{}

	data, resp, err := api.GetKafkaById(ctx, d.Id()).Execute()
	if err != nil && err.Error() == "404 Not Found" {
		d.SetId("")
		return diags
	}
	if err != nil {
		bodyBytes, ioErr := ioutil.ReadAll(resp.Body)
		if ioErr != nil {
			log.Fatal(ioErr)
		}
		return diag.Errorf("%s %s", err.Error(), string(bodyBytes))
	}
	obj, err := utils.AsMap(data)
	if err != nil {
		return diag.FromErr(errors.WithStack(err))
	}
	raw = []map[string]interface{}{obj}

	items := fixBootstrapServerHosts(raw)
	if err != nil {
		diag.FromErr(err)
	}
	if err := d.Set("kafka", items); err != nil {
		return diag.FromErr(err)
	}
	return diags
}

func kafkaCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	// Warning or errors can be collected in a slice type
	var diags diag.Diagnostics

	c := m.(*connection.KeycloakConnection)

	api := c.API().Kafka()

	items := d.Get("kafka").([]interface{})

	payload := make([]kasclient.KafkaRequestPayload, 0)

	for _, item := range items {
		kafka := item.(map[string]interface{})

		cloudProvider := kafka["cloud_provider"].(string)
		name := kafka["name"].(string)
		multiAz := kafka["multi_az"].(bool)
		region := kafka["region"].(string)

		payload = append(payload, kasclient.KafkaRequestPayload{
			CloudProvider: &cloudProvider,
			MultiAz:       &multiAz,
			Name:          name,
			Region:        &region,
		})
	}

	kr, resp, err := api.CreateKafka(ctx).Async(true).KafkaRequestPayload(payload[0]).Execute()

	if err != nil {
		bodyBytes, ioErr := ioutil.ReadAll(resp.Body)
		if ioErr != nil {
			log.Fatal(ioErr)
		}
		return diag.Errorf("%s%s", err.Error(), string(bodyBytes))
	}

	if kr.Id == nil {
		return diag.Errorf("no id provided")
	}

	d.SetId(*kr.Id)

	createStateConf := &resource.StateChangeConf{
		Delay: 5 * time.Second,
		Pending: []string{
			"accepted",
			"preparing",
			"provisioning",
		},
		Refresh: func() (interface{}, string, error) {
			c := m.(*connection.KeycloakConnection)

			api := c.API().Kafka()

			var raw []map[string]interface{}

			data, resp, err := api.GetKafkaById(ctx, *kr.Id).Execute()
			if err != nil {
				bodyBytes, ioErr := ioutil.ReadAll(resp.Body)
				if ioErr != nil {
					log.Fatal(ioErr)
				}
				return nil, "", errors.Errorf("%s%s", err.Error(), string(bodyBytes))
			}
			obj, err := utils.AsMap(data)
			if err != nil {
				return nil, "", errors.WithStack(err)
			}
			raw = []map[string]interface{}{obj}

			items := fixBootstrapServerHosts(raw)
			return items, *data.Status, nil
		},
		Target: []string{
			"ready",
		},
		Timeout:                   d.Timeout(schema.TimeoutCreate),
		MinTimeout:                5 * time.Second,
		NotFoundChecks:            0,
		ContinuousTargetOccurence: 0,
	}

	data, err := createStateConf.WaitForStateContext(ctx)
	if err != nil {
		return diag.FromErr(errors.Wrapf(err, "Error waiting for instance (%s) to be created", d.Id()))
	}
	if err := d.Set("kafka", data.([]map[string]interface{})); err != nil {
		return diag.FromErr(err)
	}
	if err != nil {
		diag.FromErr(err)
	}
	return diags
}
