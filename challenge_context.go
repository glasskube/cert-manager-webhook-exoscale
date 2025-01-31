package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	acmev1alpha1 "github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	egoscale "github.com/exoscale/egoscale/v3"
	"github.com/exoscale/egoscale/v3/credentials"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type challengeContext struct {
	k8s        *kubernetes.Clientset
	exo        *egoscale.Client
	ch         *acmev1alpha1.ChallengeRequest
	cfg        *customDNSProviderConfig
	RecordName string
	Record     *egoscale.DNSDomainRecord
	DNSDomain  *egoscale.DNSDomain
}

func NewChallengeContext(
	ctx context.Context,
	clientset *kubernetes.Clientset,
	ch *acmev1alpha1.ChallengeRequest,
) (*challengeContext, error) {
	cc := challengeContext{k8s: clientset, ch: ch}

	if err := cc.initConfig(ctx); err != nil {
		return nil, err
	}

	if err := cc.initExoClient(ctx); err != nil {
		return nil, err
	}

	if err := cc.initDomain(ctx); err != nil {
		return nil, err
	}

	if err := cc.initRecordName(); err != nil {
		return nil, err
	}

	if err := cc.initRecord(ctx); err != nil {
		return nil, err
	}

	return &cc, nil
}

func (cc *challengeContext) initConfig(ctx context.Context) error {
	log := klog.FromContext(ctx)
	if cc.ch.Config == nil {
		return nil
	}
	if err := json.Unmarshal(cc.ch.Config.Raw, &cc.cfg); err != nil {
		return fmt.Errorf("error decoding solver config: %v", err)
	}

	log.Info(fmt.Sprintf("Decoded configuration %v", cc.cfg))

	return nil
}

func (cc *challengeContext) loadClientCredentials(ctx context.Context) (*credentials.Credentials, error) {
	if apiKey, err := cc.resolveValue(ctx, cc.cfg.APIKey); err != nil {
		return nil, err
	} else if apiSecret, err := cc.resolveValue(ctx, cc.cfg.APISecret); err != nil {
		return nil, err
	} else {
		return credentials.NewStaticCredentials(apiKey, apiSecret), nil
	}
}

func (cc *challengeContext) resolveValue(ctx context.Context, v valueOrSecretRef) (string, error) {
	if v.FromSecret != nil {
		secret, err := cc.k8s.CoreV1().Secrets(cc.ch.ResourceNamespace).
			Get(ctx, v.FromSecret.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		} else if value, ok := secret.Data[v.FromSecret.Key]; !ok {
			return "", fmt.Errorf("secret %v does not have key %v", v.FromSecret.Name, v.FromSecret.Key)
		} else {
			return string(value), nil
		}
	} else {
		return v.Value, nil
	}
}

func (cc *challengeContext) initExoClient(ctx context.Context) error {
	if creds, err := cc.loadClientCredentials(ctx); err != nil {
		return err
	} else if client, err := egoscale.NewClient(creds); err != nil {
		return err
	} else {
		cc.exo = client
		return nil
	}
}

func (cc *challengeContext) initDomain(ctx context.Context) error {
	log := klog.FromContext(ctx)
	if cc.cfg.DomainID != "" {
		if domain, err := cc.exo.GetDNSDomain(ctx, cc.cfg.DomainID); err != nil {
			return err
		} else {
			cc.DNSDomain = domain
			return nil
		}
	} else {
		if result, err := cc.exo.ListDNSDomains(ctx); err != nil {
			return err
		} else {
			var found *egoscale.DNSDomain
			for _, domain := range result.DNSDomains {
				if isInZone(cc.ch.ResolvedFQDN, domain.UnicodeName) &&
					(found == nil || len(domain.UnicodeName) > len(found.UnicodeName)) {
					found = &domain
				}
			}

			if found == nil {
				return fmt.Errorf("no zone found to host FQDN %v", cc.ch.ResolvedFQDN)
			}
			log.Info(fmt.Sprintf("found domain %+v", found))
			cc.DNSDomain = found
			return nil
		}
	}
}

func isInZone(fqdn string, zone string) bool {
	return strings.HasSuffix(fqdn, normalizeZone(zone))
}

func normalizeZone(zone string) string {
	if !strings.HasPrefix(zone, ".") {
		zone = "." + zone
	}
	if !strings.HasSuffix(zone, ".") {
		zone = zone + "."
	}
	return zone
}

func (cc *challengeContext) initRecordName() error {
	if !isInZone(cc.ch.ResolvedFQDN, cc.DNSDomain.UnicodeName) {
		return fmt.Errorf("%v is not a subdomain of %v", cc.ch.ResolvedFQDN, cc.DNSDomain.UnicodeName)
	}
	cc.RecordName = strings.TrimSuffix(cc.ch.ResolvedFQDN, normalizeZone(cc.DNSDomain.UnicodeName))
	return nil
}

func (cc *challengeContext) initRecord(ctx context.Context) error {
	log := klog.FromContext(ctx).WithValues("recordName", cc.RecordName, "recordKey", cc.ch.Key)
	log.Info(fmt.Sprintf("looking for existing record in domain %+v", cc.DNSDomain))
	expectedContent := fmt.Sprintf(`"%v"`, cc.ch.Key)
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
		Content: cc.ch.Key,
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
