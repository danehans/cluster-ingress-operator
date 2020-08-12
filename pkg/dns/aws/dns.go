package aws

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"

	iov1 "github.com/openshift/api/operatoringress/v1"
	"github.com/openshift/cluster-ingress-operator/pkg/dns"
	logf "github.com/openshift/cluster-ingress-operator/pkg/log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/elb"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/aws/aws-sdk-go/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go/service/route53"

	kerrors "k8s.io/apimachinery/pkg/util/errors"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	// httpsProtocol is the name of the https protocol.
	httpsProtocol = "https"
	// Route53Service is the name of the Route 53 service.
	Route53Service = route53.ServiceName
	// ELBService is the name of the Elastic Load Balancing service.
	ELBService = elb.ServiceName
	// TaggingService is the name of the Resource Group Tagging service.
	TaggingService = resourcegroupstaggingapi.ServiceName
	// AWSBaseDomain is the base domain for all AWS regions other than China.
	AWSBaseDomain = "amazonaws.com"
	// AWSChinaBaseDomain is the base domain name for the AWS China regions.
	AWSChinaBaseDomain = "amazonaws.com.cn"
	// route53NonRegionalizedEndpoint is the non-regionalized Route 53
	// service endpoint for all regions other than "cn-north-1" and "cn-northwest-1".
	// See https://docs.aws.amazon.com/general/latest/gr/r53.html foe details.
	route53NonRegionalizedEndpoint = httpsProtocol + "://" + Route53Service + "." + AWSBaseDomain
	// route53UsEastEndpoint is the only supported regionalized (us-east-1) Route 53
	// service endpoint for all regions other than "cn-north-1" and "cn-northwest-1".
	// See https://docs.aws.amazon.com/general/latest/gr/r53.html for details.
	route53UsEastEndpoint = httpsProtocol + "://" + Route53Service + "." + endpoints.UsEast1RegionID + "." + AWSBaseDomain
	// route53ChinaEndpoint is the Route 53 service endpoint used for the
	// "cn-north-1" and "cn-northwest-1" China regions.
	// See https://docs.aws.amazon.com/general/latest/gr/r53.html for details.
	route53ChinaEndpoint = httpsProtocol + "://" + Route53Service + "." + AWSChinaBaseDomain
	// taggingNonRegionalizedEndpoint is the Resource Group Tagging service endpoint
	// without the region specified.
	// See https://docs.aws.amazon.com/general/latest/gr/arg.html for details.
	taggingNonRegionalizedEndpoint = httpsProtocol + "://" + TaggingService + "." + AWSBaseDomain
	// taggingUsEastEndpoint is the Resource Group Tagging service endpoint for
	// the us-east-1 region.
	// See https://docs.aws.amazon.com/general/latest/gr/arg.html for details.
	taggingUsEastEndpoint = httpsProtocol + "://" + TaggingService + "." + endpoints.UsEast1RegionID + "." + AWSBaseDomain
)

var (
	_   dns.Provider = &Provider{}
	log              = logf.Logger.WithName("dns")
)

// Provider is a dns.Provider for AWS Route53. It only supports DNSRecords of
// type CNAME, and the CNAME records are implemented as A records using the
// Route53 Alias feature.
//
// TODO: Records are considered owned by the manager if they exist in a managed
// zone and if their names match expectations. This is relatively dangerous
// compared to storing additional metadata (like tags or TXT records).
type Provider struct {
	elb     *elb.ELB
	elbv2   *elbv2.ELBV2
	route53 *route53.Route53
	tags    *resourcegroupstaggingapi.ResourceGroupsTaggingAPI

	config Config

	// lock protects access to everything below.
	lock sync.RWMutex

	// idsToTags caches IDs and their associated tag set. There is an assumed 1:1
	// relationship between an ID and its set of tags, and tag sets are considered
	// equal if their maps are reflect.DeepEqual.
	idsToTags map[string]map[string]string

	// lbZones is a cache of load balancer DNS names to LB hosted zone IDs.
	lbZones map[string]string
}

// Config is the necessary input to configure the manager.
type Config struct {
	// AccessID is an AWS credential.
	AccessID string
	// AccessKey is an AWS credential.
	AccessKey string
	// Region is the AWS region ELBs are created in.
	Region string
	// ServiceEndpoints is the list of AWS API endpoints to use for
	// Provider clients.
	ServiceEndpoints []ServiceEndpoint
}

// ServiceEndpoint stores the configuration of a custom url to
// override existing defaults of AWS Service API endpoints.
type ServiceEndpoint struct {
	// name is the name of the AWS service. The full list of service
	// names can be found at:
	//   https://docs.aws.amazon.com/general/latest/gr/aws-service-information.html
	Name string
	// url is a fully qualified URI that overrides the default generated
	// AWS API endpoint.
	URL string
}

func NewProvider(config Config, operatorReleaseVersion string) (*Provider, error) {
	creds := credentials.NewStaticCredentials(config.AccessID, config.AccessKey, "")
	sess, err := session.NewSessionWithOptions(session.Options{
		Config: aws.Config{
			Credentials: creds,
		},
		SharedConfigState: session.SharedConfigEnable,
	})
	if err != nil {
		return nil, fmt.Errorf("couldn't create AWS client session: %v", err)
	}
	sess.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/ingress-operator",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io ingress-operator", operatorReleaseVersion),
	})

	region, err := setRegion(&config, sess)
	if err != nil {
		return nil, err
	}

	elbCfg, err := config.getAwsConfig(region, ELBService)
	if err != nil {
		return nil, err
	}
	r53Cfg, err := config.getAwsConfig(region, Route53Service)
	if err != nil {
		return nil, err
	}
	tagCfg, err := config.getAwsConfig(region, TaggingService)
	if err != nil {
		return nil, err
	}

	return &Provider{
		elb:       elb.New(sess, elbCfg),
		elbv2:     elbv2.New(sess, elbCfg),
		route53:   route53.New(sess, r53Cfg),
		tags:      resourcegroupstaggingapi.New(sess, tagCfg),
		config:    config,
		idsToTags: map[string]map[string]string{},
		lbZones:   map[string]string{},
	}, nil
}

// setRegion sets the region from sess, if it exists, or from cfg.
// An error is returned if neither contain a region.
func setRegion(cfg *Config, sess *session.Session) (string, error) {
	r := aws.StringValue(sess.Config.Region)
	if len(r) > 0 {
		log.Info("using region from shared config", "region name", r)
	} else {
		r = cfg.Region
		log.Info("using region from operator config", "region name", r)
	}
	if len(r) == 0 {
		return "", fmt.Errorf("region is required")
	}
	return r, nil
}

// getAwsConfig returns an AWS config based on the provided manager cfg and AWS
// service. The returned config is based upon AWS Service documentation.
// Route 53: https://docs.aws.amazon.com/general/latest/gr/r53.html
// Tagging: https://docs.aws.amazon.com/general/latest/gr/arg.html
// ELB: https://docs.aws.amazon.com/general/latest/gr/elb.html
func (c *Config) getAwsConfig(region, service string) (*aws.Config, error) {
	awsCfg := aws.NewConfig()
	switch region {
	case endpoints.CnNorth1RegionID, endpoints.CnNorthwest1RegionID:
		if service == TaggingService {
			awsCfg = awsCfg.WithRegion(endpoints.CnNorthwest1RegionID)
		}
		if service == Route53Service {
			awsCfg = awsCfg.WithRegion(endpoints.CnNorthwest1RegionID)
			awsCfg = awsCfg.WithEndpoint(route53ChinaEndpoint)
		}
	default:
		if len(c.ServiceEndpoints) > 0 {
			route53Found := false
			elbFound := false
			tagFound := false
			for _, ep := range c.ServiceEndpoints {
				switch {
				case route53Found && elbFound && tagFound:
					break
				case ep.Name == Route53Service:
					route53Found = true
					if err := ValidateServiceEndpoint(ep.URL); err != nil {
						return nil, fmt.Errorf("failed to validate service endpoint %s: %v", Route53Service, err)
					}
					awsCfg = awsCfg.WithEndpoint(ep.URL)
					log.Info("using route53 custom endpoint", "url", ep.URL)
				case ep.Name == ELBService:
					elbFound = true
					if err := ValidateServiceEndpoint(ep.URL); err != nil {
						return nil, fmt.Errorf("failed to validate service endpoint %s: %v", ELBService, err)
					}
					awsCfg = awsCfg.WithEndpoint(ep.URL)
					log.Info("using elb custom endpoint", "url", ep.URL)
				case ep.Name == TaggingService:
					tagFound = true
					if err := ValidateServiceEndpoint(ep.URL); err != nil {
						return nil, fmt.Errorf("failed to validate service endpoint %s: %v", TaggingService, err)
					}
					awsCfg = awsCfg.WithEndpoint(ep.URL)
					log.Info("using group tagging custom endpoint", "url", ep.URL)
				}
			}
			if !route53Found && !elbFound && !tagFound {
				return nil, fmt.Errorf("%s, %s and %s services must be configured when using custom endpoints",
					ELBService, Route53Service, TaggingService)
			}
		} else {
			switch service {
			case TaggingService:
				// Tagging API must use us-east-1 region to return the hosted zone ID
				// of Route 53 elb.
				awsCfg = awsCfg.WithRegion(endpoints.UsEast1RegionID)
			case Route53Service:
				awsCfg = awsCfg.WithRegion(endpoints.UsEast1RegionID)
			default:
				// Use the provided region for all other services.
				awsCfg = awsCfg.WithRegion(region)
			}
		}
	}
	return awsCfg, nil
}

// ValidateServiceEndpoint validates uri as a valid URL and that it contains either
// region "us-east-1" or no region for the tagging and Route 53 service endpoints.
// Since Route 53 is not a regionalized service, the Tagging API will only return
// hosted zone resources when the region is "us-east-1".
// See https://docs.aws.amazon.com/general/latest/gr/r53.html for details.
func ValidateServiceEndpoint(uri string) error {
	u, err := url.ParseRequestURI(uri)
	if err != nil {
		return err
	}
	if strings.Contains(u.Host, Route53Service) {
		if uri != route53NonRegionalizedEndpoint && uri != route53UsEastEndpoint {
			return fmt.Errorf("invalid endpoint url %s for service %s; only %s and %s are supported", uri,
				Route53Service, route53NonRegionalizedEndpoint, route53UsEastEndpoint)
		}
	}
	if strings.Contains(u.Host, TaggingService) {
		if uri != taggingNonRegionalizedEndpoint && uri != taggingUsEastEndpoint {
			return fmt.Errorf("invalid endpoint url %s for service %s; only %s and %s are supported", uri,
				TaggingService, taggingNonRegionalizedEndpoint, taggingUsEastEndpoint)
		}
	}
	return nil
}

// getZoneID finds the ID of given zoneConfig in Route53. If an ID is already
// known, return that; otherwise, use tags to search for the zone. Returns an
// error if the zone can't be found.
func (m *Provider) getZoneID(zoneConfig configv1.DNSZone) (string, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	// If the config specifies the ID already, use it
	if len(zoneConfig.ID) > 0 {
		return zoneConfig.ID, nil
	}

	// If the ID for these tags is already cached, use it
	for id, tags := range m.idsToTags {
		if reflect.DeepEqual(tags, zoneConfig.Tags) {
			return id, nil
		}
	}

	// Look up and cache the ID for these tags.
	var id string

	// Even though we use filters when getting resources, the resources are still
	// paginated as though no filter were applied.  If the desired resource is not
	// on the first page, then GetResources will not return it.  We need to use
	// GetResourcesPages and possibly go through one or more empty pages of
	// resources till we find a resource that gets through the filters.
	var innerError error
	f := func(resp *resourcegroupstaggingapi.GetResourcesOutput, lastPage bool) (shouldContinue bool) {
		for _, zone := range resp.ResourceTagMappingList {
			zoneARN, err := arn.Parse(aws.StringValue(zone.ResourceARN))
			if err != nil {
				innerError = fmt.Errorf("failed to parse hostedzone ARN %q: %v", aws.StringValue(zone.ResourceARN), err)
				return false
			}
			elems := strings.Split(zoneARN.Resource, "/")
			if len(elems) != 2 || elems[0] != "hostedzone" {
				innerError = fmt.Errorf("got unexpected resource ARN: %v", zoneARN)
				return false
			}
			id = elems[1]
			return false
		}
		return true
	}
	tagFilters := []*resourcegroupstaggingapi.TagFilter{}
	for k, v := range zoneConfig.Tags {
		tagFilters = append(tagFilters, &resourcegroupstaggingapi.TagFilter{
			Key:    aws.String(k),
			Values: []*string{aws.String(v)},
		})
	}
	outerError := m.tags.GetResourcesPages(&resourcegroupstaggingapi.GetResourcesInput{
		ResourceTypeFilters: []*string{aws.String("route53:hostedzone")},
		TagFilters:          tagFilters,
	}, f)
	if err := kerrors.NewAggregate([]error{innerError, outerError}); err != nil {
		return id, fmt.Errorf("failed to get tagged resources: %v", err)
	}
	if len(id) == 0 {
		return id, fmt.Errorf("no matching hosted zone found")
	}

	// Update the cache
	m.idsToTags[id] = zoneConfig.Tags
	log.Info("found hosted zone using tags", "zone id", id, "tags", zoneConfig.Tags)

	return id, nil
}

// getLBHostedZone finds the hosted zone ID of an ELB whose DNS name matches the
// name parameter. Results are cached.
func (m *Provider) getLBHostedZone(name string) (string, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if id, exists := m.lbZones[name]; exists {
		return id, nil
	}

	var id string
	elbFn := func(resp *elb.DescribeLoadBalancersOutput, lastPage bool) (shouldContinue bool) {
		for _, lb := range resp.LoadBalancerDescriptions {
			dnsName := aws.StringValue(lb.DNSName)
			zoneID := aws.StringValue(lb.CanonicalHostedZoneNameID)
			log.V(2).Info("found classic load balancer", "name", aws.StringValue(lb.LoadBalancerName), "dns name", dnsName, "hosted zone ID", zoneID)
			if dnsName == name {
				id = zoneID
				return false
			}
		}
		return true
	}
	err := m.elb.DescribeLoadBalancersPages(&elb.DescribeLoadBalancersInput{}, elbFn)
	if err != nil {
		return "", fmt.Errorf("failed to describe classic load balancers: %v", err)
	}
	if len(id) == 0 {
		elbv2Fn := func(resp *elbv2.DescribeLoadBalancersOutput, lastPage bool) (shouldContinue bool) {
			for _, lb := range resp.LoadBalancers {
				dnsName := aws.StringValue(lb.DNSName)
				zoneID := aws.StringValue(lb.CanonicalHostedZoneId)
				log.V(2).Info("found network load balancer", "name", aws.StringValue(lb.LoadBalancerName), "dns name", dnsName, "hosted zone ID", zoneID)
				if dnsName == name {
					id = zoneID
					return false
				}
			}
			return true
		}
		err := m.elbv2.DescribeLoadBalancersPages(&elbv2.DescribeLoadBalancersInput{}, elbv2Fn)
		if err != nil {
			return "", fmt.Errorf("failed to describe network load balancers: %v", err)
		}
	}
	if len(id) == 0 {
		return "", fmt.Errorf("couldn't find hosted zone ID of ELB %s", name)
	}
	log.V(2).Info("associating load balancer with hosted zone", "dns name", name, "zone", id)
	m.lbZones[name] = id
	return id, nil
}

type action string

const (
	upsertAction action = "UPSERT"
	deleteAction action = "DELETE"
)

func (m *Provider) Ensure(record *iov1.DNSRecord, zone configv1.DNSZone) error {
	return m.change(record, zone, upsertAction)
}

func (m *Provider) Delete(record *iov1.DNSRecord, zone configv1.DNSZone) error {
	return m.change(record, zone, deleteAction)
}

// change will perform an action on a record. The target must correspond to the
// hostname of an ELB which will be automatically discovered.
func (m *Provider) change(record *iov1.DNSRecord, zone configv1.DNSZone, action action) error {
	if record.Spec.RecordType != iov1.CNAMERecordType {
		return fmt.Errorf("unsupported record type %s", record.Spec.RecordType)
	}
	// TODO: handle >0 targets
	domain, target := record.Spec.DNSName, record.Spec.Targets[0]
	if len(domain) == 0 {
		return fmt.Errorf("domain is required")
	}
	if len(target) == 0 {
		return fmt.Errorf("target is required")
	}

	zoneID, err := m.getZoneID(zone)
	if err != nil {
		return fmt.Errorf("failed to find hosted zone for record: %v", err)
	}

	// Find the target hosted zone of the load balancer attached to the service.
	targetHostedZoneID, err := m.getLBHostedZone(target)
	if err != nil {
		return fmt.Errorf("failed to get hosted zone for load balancer target %q: %v", target, err)
	}

	// Configure records.
	err = m.updateAlias(domain, zoneID, target, targetHostedZoneID, string(action))
	if err != nil {
		return fmt.Errorf("failed to update alias in zone %s: %v", zoneID, err)
	}
	switch action {
	case upsertAction:
		log.Info("upserted DNS record", "record", record.Spec, "zone", zone)
	case deleteAction:
		log.Info("deleted DNS record", "record", record.Spec, "zone", zone)
	}
	return nil
}

// updateAlias creates or updates an alias for domain in zoneID pointed at
// target in targetHostedZoneID.
func (m *Provider) updateAlias(domain, zoneID, target, targetHostedZoneID, action string) error {
	resp, err := m.route53.ChangeResourceRecordSets(&route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String(action),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(domain),
						Type: aws.String("A"),
						AliasTarget: &route53.AliasTarget{
							HostedZoneId:         aws.String(targetHostedZoneID),
							DNSName:              aws.String(target),
							EvaluateTargetHealth: aws.Bool(false),
						},
					},
				},
			},
		},
	})
	if err != nil {
		if action == string(deleteAction) {
			if aerr, ok := err.(awserr.Error); ok {
				if strings.Contains(aerr.Message(), "not found") {
					log.Info("record not found", "zone id", zoneID, "domain", domain, "target", target)
					return nil
				}
			}
		}
		return fmt.Errorf("couldn't update DNS record in zone %s: %v", zoneID, err)
	}
	log.Info("updated DNS record", "zone id", zoneID, "domain", domain, "target", target, "response", resp)
	return nil
}
