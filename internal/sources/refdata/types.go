package refdata

// discoverKind selects the strategy used to discover a dataset's current
// edition.
type discoverKind int

const (
	// discoverDateProbe walks a dated URL backwards (newest first) issuing
	// existence probes until a file is found. Used for CPC (monthly YYYYMM)
	// and IPC (annual YYYYMMDD on Jan 1).
	discoverDateProbe discoverKind = iota
	// discoverPageLink fetches a landing page and extracts the current edition
	// from a link via a regular expression. Used for MPEP, TMEP and the EPO
	// guidelines, which publish at non-formulaic edition strings but always
	// link the current file from a stable page.
	discoverPageLink
	// discoverStableURL uses fixed file URLs that always point at the current
	// edition; the edition string is derived from the Last-Modified header.
	// Used for the EUIPO trade mark guidelines and the WIPO PCT documents.
	discoverStableURL
	// discoverCrawl produces a ZIP by crawling an HTML manual that has no bulk
	// artifact. Used for the IP Australia manuals.
	discoverCrawl
	// discoverJSONAPI walks a JSON content API (table-of-contents tree + per-page
	// content) and archives the raw JSON into a ZIP. Used for the EUIPO trade
	// mark guidelines, which are served only via a client-rendered SPA backed by
	// such an API.
	discoverJSONAPI
	// discoverTokenFetch handles a JS-rendered manual whose section *content*
	// pages are server-rendered but whose table of contents is not: it scrapes a
	// seed page for the section identifiers (tokens) embedded in its data, then
	// fetches each section's HTML page into a ZIP. Used for the EPO Unitary
	// Patent Guidelines (a Next.js app).
	discoverTokenFetch
	// discoverIndexListing date-probes a directory's index.html (newest edition
	// first) to find the current edition, then resolves each file's actual URL by
	// matching the listing — the files carry a per-file generation-date suffix
	// (e.g. ncl-20240101-classification_texts-20250814.zip) that no fixed template
	// can predict. Used for the WIPO Nice and Locarno master files, whose
	// MasterFiles directory is an autoindex of dated zips.
	discoverIndexListing
)

// stepKind selects the cadence walked by discoverDateProbe.
type stepKind int

const (
	stepMonthly    stepKind = iota // edition token YYYYMM
	stepYearlyJan1                 // edition token YYYYMMDD, always Jan 1
)

// datasetFile is one downloadable file within a dataset. URLTemplate and
// NameTemplate may contain the edition token, which is substituted with the
// resolved edition. For discoverIndexListing datasets URLTemplate/NameTemplate
// are unused; FileNameRe instead selects the file from the directory listing.
type datasetFile struct {
	Role         string // scheme, definitions, concordance, catchword, notice, ispe, guide, ...
	URLTemplate  string
	NameTemplate string
	FileNameRe   string // discoverIndexListing: regexp matching this file's name in the listing
}

// indexSpec configures discoverIndexListing: a directory whose index.html lists
// the edition's files as (absolute) links, probed newest-edition-first.
type indexSpec struct {
	DirURLTemplate string // directory index URL, with the edition token, e.g. ".../{ed}/MasterFiles/"
}

// crawlSpec configures the HTML-manual crawler.
type crawlSpec struct {
	IndexURL   string // table-of-contents page; crawl starts here
	LinkPrefix string // follow hrefs whose path begins with this prefix
	SkipRe     string // optional: drop hrefs matching this regexp (e.g. archives, binaries)
	EditionRe  string // optional: scrape the edition from the index page (one capture group); else Last-Modified
	MaxPages   int    // hard cap on pages fetched
	ZipName    string // output filename; may contain the edition token
}

// discoverSpec configures edition discovery for a dataset.
type discoverSpec struct {
	Kind discoverKind

	// discoverDateProbe
	Step      stepKind
	MaxBack   int    // how many steps back to try before giving up
	ProbeRole string // which file's URL is probed for existence

	// discoverPageLink
	PageURL   string // landing page to scrape
	EditionRe string // regexp with exactly one capture group = edition

	// discoverStableURL: the edition is the Last-Modified date of ProbeRole's
	// file (defaults to the first file when ProbeRole is empty).

	// discoverCrawl
	Crawl *crawlSpec

	// discoverJSONAPI
	API *apiSpec

	// discoverTokenFetch
	Token *tokenSpec

	// discoverIndexListing
	Index *indexSpec
}

// apiSpec configures the EUIPO-style JSON content API walker.
type apiSpec struct {
	BaseURL       string // e.g. https://guidelines.euipo.europa.eu
	Language      string // publication language to select, e.g. "en"
	TitleContains string // select the current (non-obsolete) publication by title
	MaxNodes      int    // hard cap on toc nodes fetched
	ZipName       string // output filename; may contain the edition token
}

// tokenSpec configures the EPO-Unitary-style "enumerate then fetch" acquisition:
// the edition (year) is scraped from a landing page, the section tokens are read
// from a seed page under that year, and each token's HTML page is fetched.
type tokenSpec struct {
	BaseURL   string // e.g. https://www.epo.org/en/legal/guidelines-up
	YearRe    string // scrape the edition year from BaseURL (one capture group)
	SeedToken string // page under BaseURL/{year}/ that lists all section tokens, e.g. "index"
	TokenRe   string // matches section tokens in the seed page, e.g. section_[0-9_]+
	MaxPages  int    // hard cap on section pages fetched
	ZipName   string // output filename; may contain the edition token
}

// dataset is a single reference-data product: a logical set of files that share
// an edition and a discovery strategy.
type dataset struct {
	ID            string
	Name          string
	Description   string
	CheckSchedule string
	Files         []datasetFile
	Discover      discoverSpec
}
