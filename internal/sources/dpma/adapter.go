package dpma

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	dpmaconnect "github.com/patent-dev/dpma-connect-plus"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

const (
	SourceID            = "dpma-connect-plus"
	SourceName          = "DPMA Connect Plus"
	DefaultWeeksToFetch = 8
)

// dpmaClient abstracts the dpma-connect-plus methods used by this adapter.
// This interface enables testing without real DPMA credentials.
type dpmaClient interface {
	GetVersion(ctx context.Context, service string) (string, error)

	// Patent streaming methods
	GetDisclosureDocumentsXMLStream(ctx context.Context, year, week int, dst io.Writer) error
	GetDisclosureDocumentsPDFStream(ctx context.Context, year, week int, dst io.Writer) error
	GetPatentSpecificationsXMLStream(ctx context.Context, year, week int, dst io.Writer) error
	GetPatentSpecificationsPDFStream(ctx context.Context, year, week int, dst io.Writer) error
	GetUtilityModelsXMLStream(ctx context.Context, year, week int, dst io.Writer) error
	GetUtilityModelsPDFStream(ctx context.Context, year, week int, dst io.Writer) error
	GetEuropeanPatentSpecificationsXMLStream(ctx context.Context, year, week int, dst io.Writer) error
	GetEuropeanPatentSpecificationsPDFStream(ctx context.Context, year, week int, dst io.Writer) error
	GetPublicationDataXMLStream(ctx context.Context, year, week int, dst io.Writer) error
	GetApplicantCitationsXMLStream(ctx context.Context, year, week int, dst io.Writer) error

	// Design streaming methods
	GetDesignBibliographicDataXMLStream(ctx context.Context, year, week int, dst io.Writer) error
	GetDesignImagesStream(ctx context.Context, year, week int, dst io.Writer) error

	// Trademark streaming methods
	GetTrademarkBibDataAppliedStream(ctx context.Context, year, week int, dst io.Writer) error
	GetTrademarkBibDataRegisteredStream(ctx context.Context, year, week int, dst io.Writer) error
	GetTrademarkBibDataRejectedStream(ctx context.Context, year, week int, dst io.Writer) error
}

// Adapter implements the sources.Adapter interface for DPMA Connect Plus.
type Adapter struct {
	client          dpmaClient
	credentials     map[string]string
	downloadTimeout int
	weeksToFetch    int
}

// New creates a new DPMA Connect Plus adapter.
func New() *Adapter {
	return &Adapter{
		credentials:  make(map[string]string),
		weeksToFetch: DefaultWeeksToFetch,
	}
}

func (a *Adapter) ID() string   { return SourceID }
func (a *Adapter) Name() string { return SourceName }

func (a *Adapter) CredentialFields() []sources.CredentialField {
	return []sources.CredentialField{
		{
			Key:      "username",
			Label:    "Username",
			Type:     "text",
			Required: true,
			HelpText: "Your DPMA Connect Plus username",
		},
		{
			Key:      "password",
			Label:    "Password",
			Type:     "password",
			Required: true,
			HelpText: "Your DPMA Connect Plus password",
		},
	}
}

func (a *Adapter) Configure(cfg sources.AdapterConfig) {
	a.downloadTimeout = cfg.DownloadTimeoutSeconds
	a.client = nil
}

func (a *Adapter) SetCredentials(creds map[string]string) {
	a.credentials = creds
	a.client = nil
}

func (a *Adapter) ValidateCredentials(ctx context.Context) error {
	client, err := a.getClient()
	if err != nil {
		return err
	}
	_, err = client.GetVersion(ctx, dpmaconnect.ServicePatent)
	if err != nil {
		return sources.NewAdapterError(sources.ErrCodeAuth, "Failed to authenticate with DPMA Connect Plus", err)
	}
	return nil
}

func (a *Adapter) FetchProducts(_ context.Context) ([]sources.ProductInfo, error) {
	result := make([]sources.ProductInfo, 0, len(products))
	for _, p := range products {
		result = append(result, sources.ProductInfo{
			ExternalID:    p.ID,
			Name:          p.Name,
			Description:   p.Description,
			CheckSchedule: p.CheckSchedule,
		})
	}
	return result, nil
}

func (a *Adapter) FetchDeliveries(_ context.Context, productID string) ([]sources.DeliveryInfo, error) {
	p := getProductDef(productID)
	if p == nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNotFound, "Unknown product: "+productID, nil)
	}

	weeks := generateWeeks(a.weeksToFetch)
	result := make([]sources.DeliveryInfo, 0, len(weeks))
	for _, w := range weeks {
		result = append(result, sources.DeliveryInfo{
			ExternalID:  w.YYYYWW,
			Name:        fmt.Sprintf("%d/W%02d", w.Year, w.Week),
			PublishedAt: w.PublishedAt,
		})
	}
	return result, nil
}

func (a *Adapter) FetchFiles(_ context.Context, productID, deliveryID string) ([]sources.FileInfo, error) {
	p := getProductDef(productID)
	if p == nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNotFound, "Unknown product: "+productID, nil)
	}

	year, week, err := parseYYYYWW(deliveryID)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Invalid delivery ID", err)
	}

	releasedAt := isoWeekThursday(year, week)
	result := make([]sources.FileInfo, 0, len(p.FileTypes))
	for _, ft := range p.FileTypes {
		result = append(result, sources.FileInfo{
			ExternalID:  ft.ID + ":" + deliveryID,
			FileName:    fmt.Sprintf(ft.FileName, deliveryID),
			FileSize:    0,
			DownloadURI: productID + ":" + ft.ID + ":" + deliveryID,
			ReleasedAt:  releasedAt,
		})
	}
	return result, nil
}

func (a *Adapter) DownloadFile(ctx context.Context, file sources.FileInfo, dst io.Writer, progress sources.ProgressFunc) error {
	client, err := a.getClient()
	if err != nil {
		return err
	}

	parts := strings.SplitN(file.DownloadURI, ":", 3)
	if len(parts) != 3 {
		return sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Invalid download URI: "+file.DownloadURI, nil)
	}
	method := parts[1]
	year, week, err := parseYYYYWW(parts[2])
	if err != nil {
		return sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Invalid week in download URI", err)
	}

	pw := &progressWriter{dst: dst, progressFunc: progress}

	err = a.dispatch(ctx, client, method, year, week, pw)
	if err != nil {
		var dna *dpmaconnect.DataNotAvailableError
		if errors.As(err, &dna) {
			return sources.NewAdapterError(sources.ErrCodeNotFound, "Data not available for this week", err)
		}
		return err
	}
	return nil
}

func (a *Adapter) dispatch(ctx context.Context, client dpmaClient, method string, year, week int, dst io.Writer) error {
	switch method {
	// Patent
	case "disclosure-xml":
		return client.GetDisclosureDocumentsXMLStream(ctx, year, week, dst)
	case "disclosure-pdf":
		return client.GetDisclosureDocumentsPDFStream(ctx, year, week, dst)
	case "specifications-xml":
		return client.GetPatentSpecificationsXMLStream(ctx, year, week, dst)
	case "specifications-pdf":
		return client.GetPatentSpecificationsPDFStream(ctx, year, week, dst)
	case "utility-xml":
		return client.GetUtilityModelsXMLStream(ctx, year, week, dst)
	case "utility-pdf":
		return client.GetUtilityModelsPDFStream(ctx, year, week, dst)
	case "ep-specifications-xml":
		return client.GetEuropeanPatentSpecificationsXMLStream(ctx, year, week, dst)
	case "ep-specifications-pdf":
		return client.GetEuropeanPatentSpecificationsPDFStream(ctx, year, week, dst)
	case "publication-data-xml":
		return client.GetPublicationDataXMLStream(ctx, year, week, dst)
	case "citations-xml":
		return client.GetApplicantCitationsXMLStream(ctx, year, week, dst)
	// Design
	case "bibdata-xml":
		return client.GetDesignBibliographicDataXMLStream(ctx, year, week, dst)
	case "images":
		return client.GetDesignImagesStream(ctx, year, week, dst)
	// Trademark
	case "applied":
		return client.GetTrademarkBibDataAppliedStream(ctx, year, week, dst)
	case "registered":
		return client.GetTrademarkBibDataRegisteredStream(ctx, year, week, dst)
	case "rejected":
		return client.GetTrademarkBibDataRejectedStream(ctx, year, week, dst)
	default:
		return sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Unknown download method: "+method, nil)
	}
}

func (a *Adapter) getClient() (dpmaClient, error) {
	if a.client != nil {
		return a.client, nil
	}

	username := a.credentials["username"]
	password := a.credentials["password"]
	if username == "" || password == "" {
		return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Missing credentials", nil)
	}

	timeout := time.Duration(a.downloadTimeout) * time.Second
	if timeout < 20*time.Minute {
		timeout = 20 * time.Minute
	}

	cfg := dpmaconnect.DefaultConfig()
	cfg.Username = username
	cfg.Password = password
	cfg.Timeout = timeout

	client, err := dpmaconnect.NewClient(cfg)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeAuth, "Failed to create DPMA client", err)
	}

	a.client = client
	return client, nil
}

// progressWriter wraps an io.Writer to report download progress.
// Since DPMA does not provide file sizes, totalBytes is always 0.
type progressWriter struct {
	dst          io.Writer
	written      int64
	progressFunc sources.ProgressFunc
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.dst.Write(p)
	pw.written += int64(n)
	if pw.progressFunc != nil {
		pw.progressFunc(pw.written, 0)
	}
	return n, err
}
