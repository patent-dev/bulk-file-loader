package dpma

// fileType defines a downloadable weekly file type within a DPMA product.
type fileType struct {
	ID       string // identifier used in ExternalID and DownloadURI
	Name     string // human-readable name
	FileName string // template with %s placeholder for YYYYWW
}

// productDef defines a static DPMA product with its associated file types.
type productDef struct {
	ID            string
	Name          string
	Description   string
	CheckSchedule string
	FileTypes     []fileType
}

var products = []productDef{
	{
		ID:            "dpma-patent",
		Name:          "DPMA Patent Weekly Publications",
		Description:   "Weekly patent publication data from the German Patent and Trade Mark Office, including disclosure documents, patent specifications, utility models, and European patent specifications in XML and PDF formats.",
		CheckSchedule: "0 8 * * FRI",
		FileTypes: []fileType{
			{ID: "disclosure-xml", Name: "Disclosure Documents (XML)", FileName: "dpma-patent-disclosure-xml-%s.zip"},
			{ID: "disclosure-pdf", Name: "Disclosure Documents (PDF)", FileName: "dpma-patent-disclosure-pdf-%s.zip"},
			{ID: "specifications-xml", Name: "Patent Specifications (XML)", FileName: "dpma-patent-specifications-xml-%s.zip"},
			{ID: "specifications-pdf", Name: "Patent Specifications (PDF)", FileName: "dpma-patent-specifications-pdf-%s.zip"},
			{ID: "utility-xml", Name: "Utility Models (XML)", FileName: "dpma-patent-utility-xml-%s.zip"},
			{ID: "utility-pdf", Name: "Utility Models (PDF)", FileName: "dpma-patent-utility-pdf-%s.zip"},
			{ID: "ep-specifications-xml", Name: "European Patent Specifications (XML)", FileName: "dpma-patent-ep-specifications-xml-%s.zip"},
			{ID: "ep-specifications-pdf", Name: "European Patent Specifications (PDF)", FileName: "dpma-patent-ep-specifications-pdf-%s.zip"},
			{ID: "publication-data-xml", Name: "Publication Data (XML)", FileName: "dpma-patent-publication-data-xml-%s.zip"},
			{ID: "citations-xml", Name: "Applicant Citations (XML)", FileName: "dpma-patent-citations-xml-%s.zip"},
		},
	},
	{
		ID:            "dpma-design",
		Name:          "DPMA Design Weekly Publications",
		Description:   "Weekly design publication data from the German Patent and Trade Mark Office, including bibliographic data and design images.",
		CheckSchedule: "0 8 * * FRI",
		FileTypes: []fileType{
			{ID: "bibdata-xml", Name: "Bibliographic Data (XML)", FileName: "dpma-design-bibdata-xml-%s.zip"},
			{ID: "images", Name: "Design Images", FileName: "dpma-design-images-%s.zip"},
		},
	},
	{
		ID:            "dpma-trademark",
		Name:          "DPMA Trademark Weekly Publications",
		Description:   "Weekly trademark publication data from the German Patent and Trade Mark Office, including applied, registered, and rejected/withdrawn trademarks.",
		CheckSchedule: "0 8 * * FRI",
		FileTypes: []fileType{
			{ID: "applied", Name: "Trademarks Applied", FileName: "dpma-trademark-applied-%s.zip"},
			{ID: "registered", Name: "Trademarks Registered", FileName: "dpma-trademark-registered-%s.zip"},
			{ID: "rejected", Name: "Trademarks Rejected/Withdrawn", FileName: "dpma-trademark-rejected-%s.zip"},
		},
	},
}

func getProductDef(productID string) *productDef {
	for i := range products {
		if products[i].ID == productID {
			return &products[i]
		}
	}
	return nil
}
