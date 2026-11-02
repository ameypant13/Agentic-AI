package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// Prices looks up on-demand Linux list prices per instance type and region,
// caching them on disk: a found price for a week, a failed lookup for a day.
type Prices struct {
	path  string
	fetch map[string]func(ctx context.Context, region, instanceType string) (float64, error)

	mu    sync.Mutex
	cache map[string]priceEntry
}

type priceEntry struct {
	Hourly    float64   `json:"hourly"`
	Error     string    `json:"error,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// NewPrices uses the AWS Pricing API (needs pricing:GetProducts) and Azure's
// public Retail Prices API.
func NewPrices(dir, awsKeyID, awsSecret string) *Prices {
	_ = os.MkdirAll(dir, 0o700)
	p := &Prices{path: filepath.Join(dir, "prices.json"), cache: map[string]priceEntry{}}
	hc := &http.Client{Timeout: 30 * time.Second}
	p.fetch = map[string]func(context.Context, string, string) (float64, error){
		"azure": func(ctx context.Context, region, t string) (float64, error) { return azurePrice(ctx, hc, region, t) },
	}
	if awsKeyID != "" {
		p.fetch["aws"] = func(ctx context.Context, region, t string) (float64, error) {
			return awsPrice(ctx, awsKeyID, awsSecret, region, t)
		}
	}
	if raw, err := os.ReadFile(p.path); err == nil {
		_ = json.Unmarshal(raw, &p.cache)
	}
	return p
}

// Hourly returns the price, or 0 when it is unknown.
func (p *Prices) Hourly(ctx context.Context, cloud, region, instanceType string) float64 {
	if cloud == "" || region == "" || instanceType == "" {
		return 0
	}
	key := cloud + "|" + region + "|" + instanceType
	p.mu.Lock()
	e, ok := p.cache[key]
	p.mu.Unlock()
	fresh := ok && ((e.Error == "" && time.Since(e.FetchedAt) < 7*24*time.Hour) || (e.Error != "" && time.Since(e.FetchedAt) < 24*time.Hour))
	if fresh {
		return e.Hourly
	}
	f := p.fetch[cloud]
	if f == nil {
		return 0
	}
	price, err := f(ctx, region, instanceType)
	e = priceEntry{Hourly: price, FetchedAt: time.Now()}
	if err != nil {
		e = priceEntry{Error: err.Error(), FetchedAt: time.Now()}
	}
	p.mu.Lock()
	p.cache[key] = e
	raw, _ := json.Marshal(p.cache)
	p.mu.Unlock()
	_ = os.WriteFile(p.path, raw, 0o600)
	return e.Hourly
}

// Errors returns failed lookups, for the UI.
func (p *Prices) Errors() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]string{}
	for k, e := range p.cache {
		if e.Error != "" {
			out[k] = e.Error
		}
	}
	return out
}

func awsPrice(ctx context.Context, keyID, secret, region, instanceType string) (float64, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(keyID, secret, "")))
	if err != nil {
		return 0, err
	}
	f := func(field, value string) pricingtypes.Filter {
		return pricingtypes.Filter{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String(field), Value: aws.String(value)}
	}
	out, err := pricing.NewFromConfig(cfg).GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters: []pricingtypes.Filter{f("instanceType", instanceType), f("regionCode", region), f("operatingSystem", "Linux"),
			f("tenancy", "Shared"), f("preInstalledSw", "NA"), f("capacitystatus", "Used"), f("licenseModel", "No License required")},
		MaxResults: aws.Int32(5),
	})
	if err != nil {
		return 0, err
	}
	for _, item := range out.PriceList {
		if p, err := parseAWSPrice(item); err == nil && p > 0 {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no on-demand Linux price for %s in %s", instanceType, region)
}

// parseAWSPrice reads terms.OnDemand.*.priceDimensions.*.pricePerUnit.USD.
func parseAWSPrice(item string) (float64, error) {
	var doc struct {
		Terms struct {
			OnDemand map[string]struct {
				PriceDimensions map[string]struct {
					Unit         string            `json:"unit"`
					PricePerUnit map[string]string `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(item), &doc); err != nil {
		return 0, err
	}
	for _, term := range doc.Terms.OnDemand {
		for _, d := range term.PriceDimensions {
			if d.Unit != "Hrs" {
				continue
			}
			if v, err := strconv.ParseFloat(d.PricePerUnit["USD"], 64); err == nil && v > 0 {
				return v, nil
			}
		}
	}
	return 0, errors.New("no hourly USD price")
}

var azureRetailURL = "https://prices.azure.com/api/retail/prices"

func azurePrice(ctx context.Context, hc *http.Client, region, sku string) (float64, error) {
	filter := fmt.Sprintf("serviceName eq 'Virtual Machines' and armSkuName eq '%s' and armRegionName eq '%s' and priceType eq 'Consumption'",
		strings.ReplaceAll(sku, "'", ""), strings.ReplaceAll(region, "'", ""))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, azureRetailURL+"?$filter="+url.QueryEscape(filter), nil)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("azure retail prices: %s", resp.Status)
	}
	var doc struct {
		Items []azureItem `json:"Items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return 0, err
	}
	return pickAzurePrice(doc.Items)
}

type azureItem struct {
	RetailPrice   float64 `json:"retailPrice"`
	UnitOfMeasure string  `json:"unitOfMeasure"`
	SkuName       string  `json:"skuName"`
	ProductName   string  `json:"productName"`
}

// pickAzurePrice takes the Linux pay-as-you-go hourly price.
func pickAzurePrice(items []azureItem) (float64, error) {
	for _, it := range items {
		if it.UnitOfMeasure != "1 Hour" || strings.Contains(it.ProductName, "Windows") ||
			strings.Contains(it.SkuName, "Spot") || strings.Contains(it.SkuName, "Low Priority") || it.RetailPrice <= 0 {
			continue
		}
		return it.RetailPrice, nil
	}
	return 0, errors.New("no Linux pay-as-you-go price")
}
