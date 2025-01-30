package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	acmev1alpha1 "github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	egoscale "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type challengeContext struct {
	k8s        *kubernetes.Clientset
	exo        *egoscale.Client
	cfg        customDNSProviderConfig
	RecordName string
	RecordKey  string
	Record     *egoscale.DNSDomainRecord
	DNSDomain  *egoscale.DNSDomain
}

func NewChallengeContext(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	ch *acmev1alpha1.ChallengeRequest,
) (*challengeContext, error) {
	cc := challengeContext{k8s: clientset, RecordKey: ch.Key}

	if err := cc.initConfig(ctx, ch.Config); err != nil {
		return nil, err
	}

	if err := cc.initExoClient(ctx, ch); err != nil {
		return nil, err
	}

	if err := cc.initDomain(ctx); err != nil {
		return nil, err
	}

	if err := cc.initRecordName(ch.ResolvedFQDN); err != nil {
		return nil, err
	}

	if err := cc.initRecord(ctx); err != nil {
		return nil, err
	}

	return &cc, nil
}

func (cc *challengeContext) initConfig(ctx context.Context, cfgJSON *apiext.JSON) error {
	log := klog.FromContext(ctx)
	if cfgJSON == nil {
		return nil
	}
	if err := json.Unmarshal(cfgJSON.Raw, &cc.cfg); err != nil {
		return fmt.Errorf("error decoding solver config: %v", err)
	}

	log.Info(fmt.Sprintf("Decoded configuration %v", cc.cfg))

	return nil
}

func (cc *challengeContext) loadClientCredentials(
	ctx context.Context,
	ch *acmev1alpha1.ChallengeRequest,
) (*credentials.Credentials, error) {
	if cc.cfg.SecretName != "" {
		secret, err := cc.k8s.CoreV1().Secrets(ch.ResourceNamespace).Get(ctx, cc.cfg.SecretName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return credentials.NewStaticCredentials(string(secret.Data["apiKey"]), string(secret.Data["apiSecret"])), nil
	} else {
		return credentials.NewStaticCredentials(cc.cfg.APIKey, cc.cfg.APISecret), nil
	}
}

func (cc *challengeContext) initExoClient(ctx context.Context, ch *acmev1alpha1.ChallengeRequest) error {
	if creds, err := cc.loadClientCredentials(ctx, ch); err != nil {
		return err
	} else if client, err := egoscale.NewClient(creds); err != nil {
		return err
	} else {
		cc.exo = client
		return nil
	}
}

func (cc *challengeContext) initDomain(ctx context.Context) error {
	if domain, err := cc.exo.GetDNSDomain(ctx, cc.cfg.DomainID); err != nil {
		return err
	} else {
		cc.DNSDomain = domain
		return nil
	}
}

func (cc *challengeContext) initRecordName(fqdn string) error {
	zone := "." + cc.DNSDomain.UnicodeName
	if !strings.HasSuffix(zone, ".") {
		zone = zone + "."
	}
	if !strings.HasSuffix(fqdn, zone) {
		return fmt.Errorf("%v is not a subdomain of %v", fqdn, cc.DNSDomain.UnicodeName)
	}
	cc.RecordName = strings.TrimSuffix(fqdn, zone)
	return nil
}

func (cc *challengeContext) initRecord(ctx context.Context) error {
	log := klog.FromContext(ctx).WithValues("recordName", cc.RecordName, "recordKey", cc.RecordKey)
	log.Info(fmt.Sprintf("looking for existing record in domain %+v", cc.DNSDomain))
	expectedContent := fmt.Sprintf(`"%v"`, cc.RecordKey)
	if result, err := cc.exo.ListDNSDomainRecords(ctx, cc.DNSDomain.ID); err != nil {
		return err
	} else {
		log.Info(fmt.Sprintf("found %v records", len(result.DNSDomainRecords)))
		for _, record := range result.DNSDomainRecords {
			if record.Name == cc.RecordName && record.Content == expectedContent {
				log.Info(fmt.Sprintf("found existing record: %+v", record))
				cc.Record = &record
				return nil
			} else {
				log.V(1).Info(fmt.Sprintf("record not it: %+v", record))
			}
		}
		log.Info("existing record not found")
		return nil
	}
}

func (cc *challengeContext) CreateRecord(ctx context.Context) (*egoscale.OperationReference, error) {
	log := klog.FromContext(ctx)
	req := egoscale.CreateDNSDomainRecordRequest{
		Name:    cc.RecordName,
		Content: cc.RecordKey,
		Type:    egoscale.CreateDNSDomainRecordRequestTypeTXT,
		Ttl:     60,
	}
	log.V(1).Info(fmt.Sprintf("creating record: %+v", req))
	op, err := cc.exo.CreateDNSDomainRecord(ctx, cc.DNSDomain.ID, req)
	if err != nil {
		return nil, err
	}
	return op.Reference, nil
}

func (cc *challengeContext) DeleteRecord(ctx context.Context) error {
	log := klog.FromContext(ctx)
	log.V(1).Info(fmt.Sprintf("deleting record: %+v", cc.Record))
	_, err := cc.exo.DeleteDNSDomainRecord(ctx, cc.DNSDomain.ID, cc.Record.ID)
	return err
}
