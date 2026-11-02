// Package kube reads the Kubernetes clusters control-hub manages. It connects
// with the same cloud credentials control-hub uses (AWS_PROD_*, AZURE_PROD_*,
// and the pod's Google identity) and only ever calls get, list and logs.
package kube

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	container "google.golang.org/api/container/v1"
	"google.golang.org/api/option"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Infra is the part of a control-hub infrastructure record needed to connect.
type Infra struct {
	// GormID absorbs control-hub's numeric "ID" (gorm.Model). Without it,
	// encoding/json's case-insensitive matching would put that number into ID.
	GormID      any    `json:"ID"`
	ID          string `json:"id"`
	ClusterName string `json:"clusterName"`
	Provider    string `json:"provider"`
	Region      string `json:"region"`
	Status      string `json:"status"`
	K8sVersion  string `json:"kubernetesVersion"`
	Details     struct {
		AWS *struct {
			EKSClusterName string `json:"eksClusterName"`
		} `json:"awsDetails"`
		Azure *struct {
			ResourceGroupName string `json:"resourceGroupName"`
			AKSClusterName    string `json:"aksClusterName"`
		} `json:"azureDetails"`
		GCP *struct {
			ProjectID      string `json:"projectId"`
			GKEClusterName string `json:"gkeClusterName"`
		} `json:"gcpDetails"`
	} `json:"infrastructureDetails"`
}

// Credentials are control-hub's cloud credentials. GCP uses Application
// Default Credentials (the pod's workload identity), so it has no fields.
type Credentials struct {
	AWSAccessKeyID, AWSSecretAccessKey                           string
	AzureTenantID, AzureClientID, AzureSecret, AzureSubscription string
}

func CredentialsFromEnv() Credentials {
	return Credentials{
		AWSAccessKeyID:     os.Getenv("AWS_PROD_ACCESS_KEY_ID"),
		AWSSecretAccessKey: os.Getenv("AWS_PROD_SECRET_ACCESS_KEY"),
		AzureTenantID:      os.Getenv("AZURE_PROD_TENANT_ID"),
		AzureClientID:      os.Getenv("AZURE_PROD_CLIENT_ID"),
		AzureSecret:        os.Getenv("AZURE_PROD_CLIENT_SECRET"),
		AzureSubscription:  os.Getenv("AZURE_PROD_SUBSCRIPTION_ID"),
	}
}

// restConfig builds a client config for one cluster. Tokens are minted per
// request where they are short-lived (GKE, EKS), so a cached config never goes stale.
func restConfig(ctx context.Context, creds Credentials, in Infra) (*rest.Config, error) {
	var cfg *rest.Config
	var err error
	switch in.Provider {
	case "gcp":
		cfg, err = gkeConfig(ctx, in)
	case "aws":
		cfg, err = eksConfig(ctx, creds, in)
	case "azure":
		cfg, err = aksConfig(ctx, creds, in)
	default:
		return nil, fmt.Errorf("provider %q is not supported", in.Provider)
	}
	if err != nil {
		return nil, err
	}
	cfg.Timeout = 30 * time.Second
	cfg.QPS, cfg.Burst = 20, 40
	cfg.UserAgent = "control-hub-ai-console (read-only)"
	return cfg, nil
}

func gkeConfig(ctx context.Context, in Infra) (*rest.Config, error) {
	if in.Details.GCP == nil || in.Details.GCP.ProjectID == "" {
		return nil, fmt.Errorf("infrastructure has no GCP project")
	}
	name := in.Details.GCP.GKEClusterName
	if name == "" {
		name = in.ClusterName
	}
	ts, err := google.DefaultTokenSource(ctx, container.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("google credentials: %w", err)
	}
	svc, err := container.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	cl, err := svc.Projects.Locations.Clusters.Get(
		fmt.Sprintf("projects/%s/locations/%s/clusters/%s", in.Details.GCP.ProjectID, in.Region, name)).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get GKE cluster: %w", err)
	}
	ca, err := base64.StdEncoding.DecodeString(cl.MasterAuth.ClusterCaCertificate)
	if err != nil {
		return nil, fmt.Errorf("decode GKE CA: %w", err)
	}
	cfg := &rest.Config{Host: "https://" + cl.Endpoint, TLSClientConfig: rest.TLSClientConfig{CAData: ca}}
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return &oauth2.Transport{Source: oauth2.ReuseTokenSource(nil, ts), Base: rt}
	}
	return cfg, nil
}

func eksConfig(ctx context.Context, creds Credentials, in Infra) (*rest.Config, error) {
	if creds.AWSAccessKeyID == "" {
		return nil, fmt.Errorf("AWS_PROD_ACCESS_KEY_ID is not set")
	}
	name := in.ClusterName
	if in.Details.AWS != nil && in.Details.AWS.EKSClusterName != "" {
		name = in.Details.AWS.EKSClusterName
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(in.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(creds.AWSAccessKeyID, creds.AWSSecretAccessKey, "")))
	if err != nil {
		return nil, err
	}
	out, err := eks.NewFromConfig(awsCfg).DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
	if err != nil {
		return nil, fmt.Errorf("describe EKS cluster: %w", err)
	}
	ca, err := base64.StdEncoding.DecodeString(aws.ToString(out.Cluster.CertificateAuthority.Data))
	if err != nil {
		return nil, fmt.Errorf("decode EKS CA: %w", err)
	}
	presign := sts.NewPresignClient(sts.NewFromConfig(awsCfg))
	cfg := &rest.Config{Host: aws.ToString(out.Cluster.Endpoint), TLSClientConfig: rest.TLSClientConfig{CAData: ca}}
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return &eksTokenTransport{base: rt, cluster: name, presign: presign}
	}
	return cfg, nil
}

// eksTokenTransport adds an EKS bearer token: a presigned STS
// GetCallerIdentity URL, the same token `aws eks get-token` prints.
type eksTokenTransport struct {
	base    http.RoundTripper
	cluster string
	presign *sts.PresignClient

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (t *eksTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.current(req.Context())
	if err != nil {
		return nil, err
	}
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(r)
}

func (t *eksTokenTransport) current(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Now().After(t.expires) {
		signed, err := t.presign.PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{},
			func(o *sts.PresignOptions) {
				o.ClientOptions = append(o.ClientOptions, func(so *sts.Options) {
					so.APIOptions = append(so.APIOptions,
						smithyhttp.AddHeaderValue("x-k8s-aws-id", t.cluster),
						smithyhttp.AddHeaderValue("X-Amz-Expires", "900"))
				})
			})
		if err != nil {
			return "", fmt.Errorf("presign EKS token: %w", err)
		}
		t.token = "k8s-aws-v1." + base64.RawURLEncoding.EncodeToString([]byte(signed.URL))
		t.expires = time.Now().Add(10 * time.Minute) // tokens are valid 15 minutes
	}
	return t.token, nil
}

func aksConfig(ctx context.Context, creds Credentials, in Infra) (*rest.Config, error) {
	if creds.AzureClientID == "" || creds.AzureSubscription == "" {
		return nil, fmt.Errorf("AZURE_PROD_* credentials are not set")
	}
	if in.Details.Azure == nil || in.Details.Azure.ResourceGroupName == "" {
		return nil, fmt.Errorf("infrastructure has no Azure resource group")
	}
	cred, err := azidentity.NewClientSecretCredential(creds.AzureTenantID, creds.AzureClientID, creds.AzureSecret, nil)
	if err != nil {
		return nil, err
	}
	client, err := armcontainerservice.NewManagedClustersClient(creds.AzureSubscription, cred, nil)
	if err != nil {
		return nil, err
	}
	// Same call control-hub makes (ListClusterAdminCredentials); the kubeconfig
	// stays in memory and is never written to disk or returned by the API.
	resp, err := client.ListClusterAdminCredentials(ctx, in.Details.Azure.ResourceGroupName, in.ClusterName, nil)
	if err != nil {
		return nil, fmt.Errorf("get AKS credentials: %w", err)
	}
	if len(resp.Kubeconfigs) == 0 {
		return nil, fmt.Errorf("AKS returned no kubeconfig")
	}
	return clientcmd.RESTConfigFromKubeConfig(resp.Kubeconfigs[0].Value)
}
