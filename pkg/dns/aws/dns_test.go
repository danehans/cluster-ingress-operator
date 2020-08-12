package aws

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"testing"
)

func TestSetRegion(t *testing.T) {
	var tests = []struct {
		name, config, session, expect string
	}{
		{
			name:   "region in config and no session region",
			config: endpoints.UsEast1RegionID,
			expect: endpoints.UsEast1RegionID,
		},
		{
			name:    "region in session and no config region",
			session: endpoints.UsWest2RegionID,
			expect:  endpoints.UsWest2RegionID,
		},
		{
			name:    "region in session and config",
			config:  endpoints.UsEast1RegionID,
			session: endpoints.UsWest2RegionID,
			expect:  endpoints.UsWest2RegionID,
		},
	}

	for _, test := range tests {
		sess, err := createSession(test.session)
		if err != nil {
			t.Fatalf("test %s failed to create session: %v", test.name, err)
		}
		cfg := createMgrConfig(test.config)
		actual, err := setRegion(cfg, sess)
		if err != nil {
			t.Fatalf("test %s failed to set region: %v", test.name, err)
		}
		if actual != test.expect {
			t.Errorf("test %s failed; expected %s got %s", test.name, test.expect, actual)
		}
	}
}

func TestGetAwsConfig(t *testing.T) {
	var tests = []struct {
		name, region, service, customURL, expectedRegion, expectedEndpoint string
		expect                                                             bool
	}{
		{
			name:           "route 53 service and us-east-1 region",
			region:         endpoints.UsEast1RegionID,
			service:        Route53Service,
			expectedRegion: endpoints.UsEast1RegionID,
			expect:         true,
		},
		{
			name:           "route 53 service and us-west-2 region",
			region:         endpoints.UsWest2RegionID,
			service:        Route53Service,
			expectedRegion: endpoints.UsEast1RegionID,
			expect:         true,
		},
		{
			name:           "group tagging service and us-west-1 region",
			region:         endpoints.UsWest1RegionID,
			service:        TaggingService,
			expectedRegion: endpoints.UsEast1RegionID,
			expect:         true,
		},
		{
			name:           "elb service and us-east-2 region",
			region:         endpoints.UsEast2RegionID,
			service:        ELBService,
			expectedRegion: endpoints.UsEast2RegionID,
			expect:         true,
		},
		{
			name:             "custom endpoint for elb service and us-east-2 region",
			region:           endpoints.UsEast2RegionID,
			service:          ELBService,
			customURL:        httpsProtocol + "://" + ELBService + "." + endpoints.UsEast2RegionID + "." + AWSBaseDomain,
			expectedEndpoint: httpsProtocol + "://" + ELBService + "." + endpoints.UsEast2RegionID + "." + AWSBaseDomain,
			expect:           true,
		},
		{
			name:             "custom endpoint for route 53 service and us-east-1 region",
			region:           endpoints.UsEast1RegionID,
			service:          Route53Service,
			customURL:        httpsProtocol + "://" + Route53Service + "." + endpoints.UsEast1RegionID + "." + AWSBaseDomain,
			expectedEndpoint: httpsProtocol + "://" + Route53Service + "." + endpoints.UsEast1RegionID + "." + AWSBaseDomain,
			expect:           true,
		},
		{
			name:      "custom endpoint for route 53 service and us-east-2 region",
			region:    endpoints.UsEast2RegionID,
			service:   Route53Service,
			customURL: httpsProtocol + "://" + Route53Service + "." + endpoints.UsEast2RegionID + "." + AWSBaseDomain,
			expect:    false,
		},
		{
			name:             "custom endpoint for group tagging service and us-east-1 region",
			region:           endpoints.UsEast1RegionID,
			service:          TaggingService,
			customURL:        httpsProtocol + "://" + TaggingService + "." + endpoints.UsEast1RegionID + "." + AWSBaseDomain,
			expectedEndpoint: httpsProtocol + "://" + TaggingService + "." + endpoints.UsEast1RegionID + "." + AWSBaseDomain,
			expect:           true,
		},
		{
			name:      "custom endpoint for group tagging service and us-east-2 region",
			region:    endpoints.UsEast2RegionID,
			service:   TaggingService,
			customURL: httpsProtocol + "://" + TaggingService + "." + endpoints.UsEast2RegionID + "." + AWSBaseDomain,
			expect:    false,
		},
		{
			name:             "custom endpoint for elb service and us-east-1 region",
			region:           endpoints.UsEast1RegionID,
			service:          ELBService,
			customURL:        httpsProtocol + "://" + ELBService + "." + endpoints.UsEast1RegionID + "." + AWSBaseDomain,
			expectedEndpoint: httpsProtocol + "://" + ELBService + "." + endpoints.UsEast1RegionID + "." + AWSBaseDomain,
			expect:           true,
		},
		{
			name:             "custom endpoint for elb service and us-east-2 region",
			region:           endpoints.UsEast2RegionID,
			service:          ELBService,
			customURL:        httpsProtocol + "://" + ELBService + "." + "foo" + "." + AWSBaseDomain,
			expectedEndpoint: httpsProtocol + "://" + ELBService + "." + "foo" + "." + AWSBaseDomain,
			expect:           true,
		},
	}

	for _, test := range tests {
		haveEps := func() bool { return len(test.customURL) > 0 }
		mgrCfg := createMgrConfig(test.region)
		if haveEps() {
			mgrCfg.ServiceEndpoints = []ServiceEndpoint{{Name: test.service, URL: test.customURL}}
		}
		actual, err := mgrCfg.getAwsConfig(mgrCfg.Region, test.service)
		switch {
		case err != nil && test.expect:
			t.Fatalf("test %s failed to get aws config: %v", test.name, err)
		case err == nil && !test.expect:
			t.Fatalf("test %s expected to fail but didn't", test.name)
		case haveEps() && test.expect:
			ep := aws.StringValue(actual.Endpoint)
			if ep != test.expectedEndpoint && test.expect {
				t.Errorf("test %s failed; expected endpoint %s got %s", test.name, test.expectedEndpoint, ep)
			}
		case !haveEps() && test.expect:
			region := aws.StringValue(actual.Region)
			if region != test.expectedRegion && test.expect {
				t.Errorf("test %s failed; expected region %s got %s", test.name, test.expectedRegion, region)
			}
		}
	}
}

func createSession(region string) (*session.Session, error) {
	creds := credentials.NewStaticCredentials("creds", "secret", "")
	sess, err := session.NewSessionWithOptions(session.Options{
		Config: aws.Config{
			Credentials: creds,
		},
		SharedConfigState: session.SharedConfigEnable,
	})
	if err != nil {
		return nil, fmt.Errorf("couldn't create AWS client session: %v", err)
	}
	if len(region) > 0 {
		sess.Config.Region = &region
	} else {
		sess.Config.Region = nil
	}
	sess.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/ingress-operator",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io ingress-operator", "v1"),
	})
	return sess, nil
}

func createMgrConfig(region string) *Config {
	cfg := &Config{}
	if len(region) > 0 {
		cfg.Region = region
	}
	return cfg
}
