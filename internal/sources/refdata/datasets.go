package refdata

// This file declares the concrete reference-data datasets. URLs were verified
// against the live sources; edition discovery keeps them current without code
// changes for the date-probe and page-link datasets.

const (
	cpcBulk    = "https://www.cooperativepatentclassification.org/sites/default/files/cpc/bulk/"
	ipcMasters = "https://www.wipo.int/classifications/data/ipc/ITSupport_and_download_area/{ed}/MasterFiles/"
	usptoDocs  = "https://www.uspto.gov/sites/default/files/documents/"
	// WIPO Nice (NCL) and Locarno (LOC) master-file directory listings of dated
	// zips, probed annually (Jan 1) newest-first.
	niceMasters = "https://www.wipo.int/classifications/data/nice/ITSupport_and_download_area/{ed}/MasterFiles/"
	locMasters  = "https://www.wipo.int/classifications/data/locarno/ITSupport_and_download_area/{ed}/MasterFiles/"
)

// classificationDatasets returns CPC and IPC. Both pull the definition
// (description) files in addition to the scheme tree, so downstream consumers
// get titles and full definitions, not just the hierarchy.
func classificationDatasets() []dataset {
	return []dataset{
		{
			ID:            "cpc",
			Name:          "Cooperative Patent Classification (CPC)",
			Description:   "CPC bulk data from the joint EPO/USPTO scheme: the classification scheme (tree), full definitions (descriptions), and the notice of changes. Published roughly monthly.",
			CheckSchedule: "0 7 5 * *", // 07:00 on the 5th of each month
			Files: []datasetFile{
				{Role: "scheme", URLTemplate: cpcBulk + "CPCSchemeXML{ed}.zip", NameTemplate: "CPCSchemeXML{ed}.zip"},
				{Role: "definitions", URLTemplate: cpcBulk + "FullCPCDefinitionXML{ed}.zip", NameTemplate: "FullCPCDefinitionXML{ed}.zip"},
				{Role: "notice", URLTemplate: cpcBulk + "CPCNoticeOfChangeXMLArtefact{ed}.zip", NameTemplate: "CPCNoticeOfChangeXMLArtefact{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind:      discoverDateProbe,
				Step:      stepMonthly,
				MaxBack:   18,
				ProbeRole: "scheme",
			},
		},
		{
			ID:            "ipc",
			Name:          "International Patent Classification (IPC)",
			Description:   "WIPO IPC master files: the classification scheme (tree), definitions (descriptions), the IPC-CPC concordance list, and the catchword index. Published annually on 1 January.",
			CheckSchedule: "0 7 5 1 *", // 07:00 on 5 January
			Files: []datasetFile{
				{Role: "scheme", URLTemplate: ipcMasters + "ipc_scheme_{ed}.zip", NameTemplate: "ipc_scheme_{ed}.zip"},
				{Role: "definitions", URLTemplate: ipcMasters + "ipc_definitions_{ed}.zip", NameTemplate: "ipc_definitions_{ed}.zip"},
				{Role: "concordance", URLTemplate: ipcMasters + "ipc_concordancelist_{ed}.zip", NameTemplate: "ipc_concordancelist_{ed}.zip"},
				{Role: "catchword", URLTemplate: ipcMasters + "ipc_catchwordindex_{ed}.zip", NameTemplate: "ipc_catchwordindex_{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind:      discoverDateProbe,
				Step:      stepYearlyJan1,
				MaxBack:   6,
				ProbeRole: "scheme",
			},
		},
		{
			ID:            "nice",
			Name:          "Nice Classification (Goods & Services)",
			Description:   "WIPO Nice Classification master files (the international classification of goods & services for trademarks): the class top structure (45 classes + basic numbers) and the classification texts (class headings, full explanatory notes, and ~10k accepted goods/services indications). Published annually on 1 January.",
			CheckSchedule: "0 7 8 1 *", // 07:00 on 8 January
			Files: []datasetFile{
				{Role: "top_structure", FileNameRe: `^ncl-\d{8}-classification_top_structure-\d{8}\.zip$`},
				{Role: "texts", FileNameRe: `^ncl-\d{8}-classification_texts-\d{8}\.zip$`},
			},
			Discover: discoverSpec{
				Kind:    discoverIndexListing,
				Step:    stepYearlyJan1,
				MaxBack: 6,
				Index:   &indexSpec{DirURLTemplate: niceMasters},
			},
		},
		{
			ID:            "locarno",
			Name:          "Locarno Classification (Industrial Designs)",
			Description:   "WIPO Locarno Classification master files (the international classification for industrial designs): the class/subclass top structure (32 classes + subclasses) and the classification texts (class & subclass headings, explanatory notes, and ~5k product indications). Published annually on 1 January.",
			CheckSchedule: "0 7 8 1 *", // 07:00 on 8 January
			Files: []datasetFile{
				{Role: "top_structure", FileNameRe: `^loc-\d{8}-classification_top_structure-\d{8}\.zip$`},
				{Role: "texts", FileNameRe: `^loc-\d{8}-classification_texts-\d{8}\.zip$`},
			},
			Discover: discoverSpec{
				Kind:    discoverIndexListing,
				Step:    stepYearlyJan1,
				MaxBack: 6,
				Index:   &indexSpec{DirURLTemplate: locMasters},
			},
		},
		{
			ID:   "jp-fterm",
			Name: "JPO F-term Classification",
			// JPO never published an F-term bulk file; the whole scheme exists only
			// as PMGS HTML on J-PlatPat (which, unlike jpo.go.jp, is not Akamai-bot-
			// protected). We crawl the English F_TERM tree: themeGroup index →
			// per-group theme list → per-theme F-term list (viewpoints + terms).
			Description:   "JPO F-term faceted classification (theme → viewpoint → term), crawled from the J-PlatPat PMGS English HTML and archived as a ZIP. No bulk file exists; English coverage is partial.",
			CheckSchedule: "0 7 8 * *", // monthly, 8th at 07:00
			Files: []datasetFile{
				{Role: "scheme", NameTemplate: "jp-fterm-{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind: discoverCrawl,
				Crawl: &crawlSpec{
					IndexURL:   "https://www.j-platpat.inpit.go.jp/cache/classify/patent/PMGS_HTML/jpp/F_TERM/en/themeGroup/themeGroup_en.html",
					LinkPrefix: "/cache/classify/patent/PMGS_HTML/jpp/F_TERM/en/",
					SkipRe:     `(?i)(/ja/|\.(css|js|png|gif|jpe?g|ico|svg)$)`,
					MaxPages:   6000,
				},
			},
		},
	}
}

// manualDatasets returns the examination manuals and guidelines.
func manualDatasets() []dataset {
	return []dataset{
		{
			ID:            "mpep",
			Name:          "USPTO MPEP",
			Description:   "Manual of Patent Examining Procedure (USPTO). No HTML bulk artifact exists, so the HTML manual is crawled (chapter TOCs into per-section pages) and archived as a ZIP. The 9th-edition revision (e.g. e9r-01-2024) is scraped from the index page.",
			CheckSchedule: "0 7 * * 1", // weekly, Monday 07:00
			Files: []datasetFile{
				{Role: "manual", NameTemplate: "mpep-{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind: discoverCrawl,
				Crawl: &crawlSpec{
					IndexURL:   "https://www.uspto.gov/web/offices/pac/mpep/",
					LinkPrefix: "/web/offices/pac/mpep/",
					SkipRe:     `(?i)(/old/|\.(zip|pdf|jpe?g|png|gif|css|js)$)`,
					EditionRe:  `(e\d+r-[0-9-]+)\.zip`,
					MaxPages:   2000,
				},
			},
		},
		{
			ID:            "tmep",
			Name:          "USPTO TMEP",
			Description:   "Trademark Manual of Examining Procedure (USPTO), HTML edition ZIP. The current edition is discovered from the TMEP archives page.",
			CheckSchedule: "0 7 * * 1",
			Files: []datasetFile{
				{Role: "manual", URLTemplate: usptoDocs + "tmep-{ed}-html.zip", NameTemplate: "tmep-{ed}-html.zip"},
			},
			Discover: discoverSpec{
				Kind:      discoverPageLink,
				PageURL:   "https://www.uspto.gov/trademarks/guides-and-manuals/tmep-archives",
				EditionRe: `tmep-([a-z]+[0-9]{4})-html\.zip`,
			},
		},
		{
			ID:            "euipo-tm",
			Name:          "EUIPO Trade Mark Guidelines",
			Description:   "Guidelines for Examination of EU Trade Marks (EUIPO), current edition. Served via a JSON content API behind the SPA at guidelines.euipo.europa.eu; the loader walks the toc tree + fetches each page and archives the raw JSON. The current (non-obsolete) EN publication is discovered from api/publications.",
			CheckSchedule: "0 7 * * 1",
			Files: []datasetFile{
				{Role: "manual", NameTemplate: "euipo-tm-{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind: discoverJSONAPI,
				API: &apiSpec{
					BaseURL:       "https://guidelines.euipo.europa.eu",
					Language:      "en",
					TitleContains: "trade mark guidelines",
					MaxNodes:      8000, // the 2026-07-01 edition has 1988 nodes
				},
			},
		},
		{
			ID:            "wipo-ispe",
			Name:          "WIPO PCT ISPE Guidelines",
			Description:   "WIPO PCT International Search and Preliminary Examination (ISPE) Guidelines. Server-rendered per-paragraph-range HTML, crawled into a ZIP. (The PCT Applicant's Guide is a JSF app and is deferred.)",
			CheckSchedule: "0 7 * * 1",
			Files: []datasetFile{
				{Role: "manual", NameTemplate: "wipo-ispe-{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind: discoverCrawl,
				Crawl: &crawlSpec{
					IndexURL:   "https://www.wipo.int/en/web/pct-system/texts/ispe/index",
					LinkPrefix: "/en/web/pct-system/texts/ispe/",
					SkipRe:     `(?i)\.(pdf|zip|jpe?g|png|gif|css|js)$`,
					MaxPages:   300,
				},
			},
		},
		{
			ID:            "epo-epc",
			Name:          "EPO Guidelines for Examination (EPC)",
			Description:   "EPO Guidelines for Examination (EPC). Server-rendered per-section HTML, crawled into a ZIP (no bulk artifact). The edition year is scraped from the guidelines index. (PCT-EPO and Unitary guidelines follow the same pattern.)",
			CheckSchedule: "0 7 * * 1",
			Files: []datasetFile{
				{Role: "manual", NameTemplate: "epo-epc-{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind: discoverCrawl,
				Crawl: &crawlSpec{
					IndexURL:   "https://www.epo.org/en/legal/guidelines-epc",
					LinkPrefix: "/en/legal/guidelines-epc/",
					SkipRe:     `(?i)(/archive|/consultation|/responses|/revision-cycle|\.(pdf|zip|jpe?g|png|gif|css|js)$)`,
					EditionRe:  `guidelines-epc/(\d{4})/`,
					MaxPages:   6000,
				},
			},
		},
		{
			ID:            "epo-pct",
			Name:          "EPO Guidelines for Search and Examination as PCT Authority",
			Description:   "EPO PCT-EPO Guidelines. Same server-rendered per-section HTML as the EPC guidelines, crawled into a ZIP.",
			CheckSchedule: "0 7 * * 1",
			Files: []datasetFile{
				{Role: "manual", NameTemplate: "epo-pct-{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind: discoverCrawl,
				Crawl: &crawlSpec{
					IndexURL:   "https://www.epo.org/en/legal/guidelines-pct",
					LinkPrefix: "/en/legal/guidelines-pct/",
					SkipRe:     `(?i)(/archive|/consultation|/responses|/revision-cycle|\.(pdf|zip|jpe?g|png|gif|css|js)$)`,
					EditionRe:  `guidelines-pct/(\d{4})/`,
					MaxPages:   6000,
				},
			},
		},
		{
			ID:            "epo-up",
			Name:          "EPO Unitary Patent Guidelines",
			Description:   "EPO Guidelines for the Unitary Patent. A Next.js app: the table of contents is client-rendered, but each /{year}/section_N_M_K content page is server-rendered HTML. The loader scrapes the section tokens from the year's index page and fetches each section page into a ZIP. The edition year is scraped from the landing page.",
			CheckSchedule: "0 7 * * 1",
			Files: []datasetFile{
				{Role: "manual", NameTemplate: "epo-up-{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind: discoverTokenFetch,
				Token: &tokenSpec{
					BaseURL:   "https://www.epo.org/en/legal/guidelines-up",
					YearRe:    `guidelines-up/(\d{4})/`,
					SeedToken: "index",
					TokenRe:   `section_[0-9_]+`,
					MaxPages:  1000,
				},
			},
		},
		{
			ID:            "ipa-patent",
			Name:          "IP Australia Patent Manual",
			Description:   "Patent Manual of Practice and Procedure (IP Australia). No bulk artifact exists, so the HTML manual is crawled and archived as a ZIP of its section pages.",
			CheckSchedule: "0 7 1 * *", // monthly, 1st at 07:00
			Files: []datasetFile{
				{Role: "manual", NameTemplate: "ipa-patent-manual-{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind: discoverCrawl,
				Crawl: &crawlSpec{
					IndexURL:   "https://manuals.ipaustralia.gov.au/patent",
					LinkPrefix: "/patent/",
					MaxPages:   1500,
				},
			},
		},
		{
			ID:            "ipa-trademark",
			Name:          "IP Australia Trade Marks Manual",
			Description:   "Trade Marks Examiners Manual (IP Australia). Crawled from HTML and archived as a ZIP of its section pages.",
			CheckSchedule: "0 7 1 * *",
			Files: []datasetFile{
				{Role: "manual", NameTemplate: "ipa-trade-marks-manual-{ed}.zip"},
			},
			Discover: discoverSpec{
				Kind: discoverCrawl,
				Crawl: &crawlSpec{
					IndexURL:   "https://manuals.ipaustralia.gov.au/trademark",
					LinkPrefix: "/trademark/",
					MaxPages:   1500,
				},
			},
		},
	}
}
