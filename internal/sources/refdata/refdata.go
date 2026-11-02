// Package refdata provides bulk-file-loader source adapters for public
// reference data that is distributed as versioned files over anonymous HTTPS:
// patent classification schemes (CPC, IPC) and patent/trademark examination
// manuals (MPEP, TMEP, EPO/EUIPO/WIPO guidelines, IP Australia manuals).
//
// Unlike the office API adapters (EPO BDDS, USPTO ODP, DPMA Connect Plus),
// these sources need no credentials and expose no catalog API. Each dataset
// declares how to discover its current edition (probe a dated URL, scrape a
// landing page, or read a Last-Modified header) and how to build the file URLs
// for that edition. The implementation uses only the Go standard library.
package refdata

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// listingHrefRe extracts link targets from a directory listing page. Unlike
// the crawler's hrefRe it also tolerates unquoted hrefs (href=https://.../x.zip>).
var listingHrefRe = regexp.MustCompile(`(?i)href=["']?([^"' >]+)`)

const (
	// ClassificationsID / ManualsID are the source identifiers surfaced in the
	// CLI, web UI and database.
	ClassificationsID   = "patent-classifications"
	ClassificationsName = "Patent Classifications (CPC, IPC)"
	ManualsID           = "examination-manuals"
	ManualsName         = "Examination Manuals & Guidelines"

	// metadataTimeout bounds the lightweight metadata calls (edition discovery,
	// existence probes, landing-page scrapes). File downloads are not bounded by
	// this; they run under the context deadline supplied by the downloader.
	metadataTimeout = 60 * time.Second

	// editionToken is replaced with the resolved edition in URL and filename
	// templates.
	editionToken = "{ed}"
)

// httpDoer is the subset of *http.Client used by the adapter. It is an
// injection seam so tests can drive the adapter against an httptest server or
// a mock transport without reaching the network.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Adapter implements sources.Adapter for a fixed set of credential-less,
// file-based reference-data datasets. Construct one with NewClassifications or
// NewManuals.
type Adapter struct {
	id       string
	name     string
	datasets []dataset
	byID     map[string]*dataset

	client httpDoer
	now    func() time.Time
}

// newAdapter wires a set of datasets into an Adapter with the default HTTP
// client and clock.
func newAdapter(id, name string, datasets []dataset) *Adapter {
	a := &Adapter{
		id:       id,
		name:     name,
		datasets: datasets,
		byID:     make(map[string]*dataset, len(datasets)),
		// No client-level timeout: large downloads (e.g. the 832 MB CPC
		// definitions) must be free to run for as long as the downloader's
		// context allows. Per-request deadlines are applied where appropriate.
		client: &http.Client{},
		now:    time.Now,
	}
	for i := range a.datasets {
		a.byID[a.datasets[i].ID] = &a.datasets[i]
	}
	return a
}

// NewClassifications returns the adapter for patent classification schemes
// (CPC and IPC), including their definition/description files, not only the
// scheme tree.
func NewClassifications() *Adapter {
	return newAdapter(ClassificationsID, ClassificationsName, classificationDatasets())
}

// NewManuals returns the adapter for examination manuals and guidelines.
func NewManuals() *Adapter {
	return newAdapter(ManualsID, ManualsName, manualDatasets())
}

func (a *Adapter) ID() string   { return a.id }
func (a *Adapter) Name() string { return a.name }

// CredentialFields reports that these sources need no credentials.
func (a *Adapter) CredentialFields() []sources.CredentialField { return nil }

// SetCredentials is a no-op; these sources are unauthenticated.
func (a *Adapter) SetCredentials(map[string]string) {}

// ValidateCredentials always succeeds; there are no credentials to validate.
func (a *Adapter) ValidateCredentials(context.Context) error { return nil }

// Configure satisfies sources.ConfigurableAdapter. The download timeout is
// applied by the downloader via the request context, so nothing is stored here.
func (a *Adapter) Configure(sources.AdapterConfig) {}

// FetchProducts returns one product per dataset.
func (a *Adapter) FetchProducts(context.Context) ([]sources.ProductInfo, error) {
	result := make([]sources.ProductInfo, 0, len(a.datasets))
	for i := range a.datasets {
		d := &a.datasets[i]
		result = append(result, sources.ProductInfo{
			ExternalID:    d.ID,
			Name:          d.Name,
			Description:   d.Description,
			CheckSchedule: d.CheckSchedule,
		})
	}
	return result, nil
}

// FetchDeliveries discovers the current edition of a dataset and returns it as
// a single delivery.
func (a *Adapter) FetchDeliveries(ctx context.Context, productID string) ([]sources.DeliveryInfo, error) {
	d := a.byID[productID]
	if d == nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNotFound, "Unknown product: "+productID, nil)
	}

	edition, publishedAt, err := a.resolveEdition(ctx, d)
	if err != nil {
		return nil, err
	}

	info := sources.DeliveryInfo{
		ExternalID: edition,
		Name:       d.Name + " " + edition,
	}
	if !publishedAt.IsZero() {
		info.PublishedAt = publishedAt
	}
	return []sources.DeliveryInfo{info}, nil
}

// FetchFiles builds the file list for a dataset edition. For most datasets no
// network access is needed: file URLs are derived from the edition by template
// substitution. discoverIndexListing datasets are the exception: their file
// names carry an unpredictable generation-date suffix, so the actual URLs are
// resolved from the directory listing.
func (a *Adapter) FetchFiles(ctx context.Context, productID, deliveryID string) ([]sources.FileInfo, error) {
	d := a.byID[productID]
	if d == nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNotFound, "Unknown product: "+productID, nil)
	}
	if deliveryID == "" {
		return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Empty edition", nil)
	}

	if d.Discover.Kind == discoverIndexListing {
		return a.listingFiles(ctx, d, deliveryID)
	}

	result := make([]sources.FileInfo, 0, len(d.Files))
	for _, f := range d.Files {
		role := f.Role
		if role == "" {
			role = "file"
		}
		result = append(result, sources.FileInfo{
			ExternalID:  d.ID + ":" + role + ":" + deliveryID,
			FileName:    subst(f.NameTemplate, deliveryID),
			DownloadURI: subst(f.URLTemplate, deliveryID),
			// For date-based editions (CPC YYYYMM, IPC YYYYMMDD) this records
			// the release date; for non-date editions it stays zero.
			ReleasedAt: editionDate(deliveryID),
		})
	}
	return result, nil
}

// listingFiles resolves a discoverIndexListing dataset's files by fetching the
// edition's directory index and matching each declared file's FileNameRe against
// the listed links. The matched link, resolved against the listing URL, is the
// download URL and its base name is the stored file name. A declared file with no match is an error, so a
// missing master file fails loudly rather than importing a partial edition.
func (a *Adapter) listingFiles(ctx context.Context, d *dataset, edition string) ([]sources.FileInfo, error) {
	dirURL := subst(d.Discover.Index.DirURLTemplate, edition)
	body, err := a.getBody(ctx, dirURL, maxPageBytes)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(dirURL)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad listing URL: "+dirURL, err)
	}
	var links []string
	for _, m := range listingHrefRe.FindAllStringSubmatch(string(body), -1) {
		ref, err := url.Parse(m[1])
		if err != nil {
			continue
		}
		links = append(links, base.ResolveReference(ref).String())
	}

	result := make([]sources.FileInfo, 0, len(d.Files))
	for _, f := range d.Files {
		re, err := regexp.Compile(f.FileNameRe)
		if err != nil {
			return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad FileNameRe for "+d.ID+":"+f.Role, err)
		}
		var matchURL, matchName string
		for _, link := range links {
			base := path.Base(link)
			if re.MatchString(base) {
				matchURL, matchName = link, base // last match wins (newest generation date)
			}
		}
		if matchURL == "" {
			return nil, sources.NewAdapterError(sources.ErrCodeNotFound,
				"no "+f.Role+" file matching /"+f.FileNameRe+"/ in "+dirURL, nil)
		}
		role := f.Role
		if role == "" {
			role = "file"
		}
		result = append(result, sources.FileInfo{
			ExternalID:  d.ID + ":" + role + ":" + edition,
			FileName:    matchName,
			DownloadURI: matchURL,
			ReleasedAt:  editionDate(edition),
		})
	}
	return result, nil
}

// DownloadFile streams a single file, or for crawl datasets produces a ZIP of
// the crawled manual.
func (a *Adapter) DownloadFile(ctx context.Context, file sources.FileInfo, dst io.Writer, progress sources.ProgressFunc) error {
	datasetID, _, _, err := parseExternalID(file.ExternalID)
	if err != nil {
		return err
	}
	d := a.byID[datasetID]
	if d == nil {
		return sources.NewAdapterError(sources.ErrCodeNotFound, "Unknown dataset in file: "+file.ExternalID, nil)
	}

	switch d.Discover.Kind {
	case discoverCrawl:
		return a.crawl(ctx, d.Discover.Crawl, dst, progress)
	case discoverJSONAPI:
		return a.fetchEUIPO(ctx, d.Discover.API, dst, progress)
	case discoverTokenFetch:
		return a.fetchTokenPages(ctx, d.Discover.Token, dst, progress)
	}
	return a.downloadURL(ctx, file.DownloadURI, dst, progress)
}

// parseExternalID splits a "dataset:role:edition" external ID.
func parseExternalID(id string) (datasetID, role, edition string, err error) {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 {
		return "", "", "", sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Invalid file ID: "+id, nil)
	}
	return parts[0], parts[1], parts[2], nil
}

// subst replaces the edition token in a template.
func subst(template, edition string) string {
	return strings.ReplaceAll(template, editionToken, edition)
}

// ensure Adapter satisfies the interfaces at compile time.
var (
	_ sources.Adapter             = (*Adapter)(nil)
	_ sources.ConfigurableAdapter = (*Adapter)(nil)
)
