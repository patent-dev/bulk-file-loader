package refdata

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// EUIPO trade mark guidelines are served by a client-rendered SPA backed by a
// JSON API: api/publications lists editions, api/toc/{pub}[/{node}] walks the
// section tree, api/page/{pub}/{pageId} returns a section's content. The loader
// walks that API and archives the raw JSON (a toc.json index + one page-*.json
// per content node) into a ZIP for the importer to parse.

type euipoPublication struct {
	ID                    string   `json:"Id"`
	Title                 string   `json:"Title"`
	Language              string   `json:"Language"`
	EntryIntoForce        string   `json:"EntryIntoForce"`
	ProductReleaseVersion []string `json:"ProductReleaseVersion"`
	IsPubObsolete         *string  `json:"IsPubObsolete"`
}

type euipoTocNode struct {
	ID            string  `json:"Id"`
	Title         string  `json:"Title"`
	URL           *string `json:"Url"`
	HasChildNodes bool    `json:"HasChildNodes"`
}

// euipoCollectedNode is what the loader records per toc node for the importer.
type euipoCollectedNode struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
	Title    string `json:"title"`
	PageID   string `json:"pageId,omitempty"`
}

// resolveEUIPOPublication finds the current (non-obsolete) publication matching
// the spec's language and title, returning its id and edition (the entry-into-
// force date, YYYYMMDD).
func (a *Adapter) resolveEUIPOPublication(ctx context.Context, spec *apiSpec) (pubID, edition string, err error) {
	body, err := a.getBody(ctx, spec.BaseURL+"/api/publications", maxPageBytes)
	if err != nil {
		return "", "", err
	}
	var pubs []euipoPublication
	if err := json.Unmarshal(body, &pubs); err != nil {
		return "", "", sources.NewAdapterError(sources.ErrCodeNetwork, "decode EUIPO publications", err)
	}
	for _, p := range pubs {
		if !strings.EqualFold(p.Language, spec.Language) || p.IsPubObsolete != nil {
			continue
		}
		if !strings.Contains(strings.ToLower(p.Title), strings.ToLower(spec.TitleContains)) {
			continue
		}
		ed := p.EntryIntoForce
		if t, perr := time.Parse("2006-01-02T15:04:05", ed); perr == nil {
			ed = t.Format("20060102")
		}
		return p.ID, ed, nil
	}
	return "", "", sources.NewAdapterError(sources.ErrCodeNotFound, "no current EUIPO publication for "+spec.TitleContains, nil)
}

// fetchEUIPO walks the current publication's toc tree and fetches each content
// page, writing toc.json plus page-{pageId}.json into the ZIP at dst.
func (a *Adapter) fetchEUIPO(ctx context.Context, spec *apiSpec, dst io.Writer, progress sources.ProgressFunc) error {
	pubID, _, err := a.resolveEUIPOPublication(ctx, spec)
	if err != nil {
		return err
	}
	return a.archiveEUIPO(ctx, spec, pubID, "", dst, progress)
}

// archiveEUIPO archives the toc subtree below rootID ("" for the whole
// publication) and its content pages into the ZIP at dst.
func (a *Adapter) archiveEUIPO(ctx context.Context, spec *apiSpec, pubID, rootID string, dst io.Writer, progress sources.ProgressFunc) error {

	// Progress is the archive bytes written; the final size is unknown up front.
	zw := zip.NewWriter(&progressWriter{dst: dst, total: -1, fn: progress})
	var collected []euipoCollectedNode
	seenPage := map[string]struct{}{}

	var walk func(nodeID, parentID string) error
	walk = func(nodeID, parentID string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		url := spec.BaseURL + "/api/toc/" + pubID
		if nodeID != "" {
			url += "/" + nodeID
		}
		body, err := a.getBodyTimed(ctx, url, maxPageBytes)
		if err != nil {
			return err
		}
		var nodes []euipoTocNode
		if err := json.Unmarshal(body, &nodes); err != nil {
			return sources.NewAdapterError(sources.ErrCodeNetwork, "decode EUIPO toc "+nodeID, err)
		}
		for _, n := range nodes {
			// Skip hidden/admin nodes with a blank title (e.g. the second root
			// "t100000000") and their subtrees; they carry no real content.
			if strings.TrimSpace(n.Title) == "" {
				continue
			}
			// The cap is set above the guidelines' real size; exceeding it means
			// the tree grew unexpectedly, so fail rather than archive a partial.
			if spec.MaxNodes > 0 && len(collected) >= spec.MaxNodes {
				return sources.NewAdapterError(sources.ErrCodeNetwork,
					fmt.Sprintf("EUIPO toc exceeds the %d-node cap: manual would be incomplete", spec.MaxNodes), nil)
			}
			pageID := euipoPageID(n.URL)
			collected = append(collected, euipoCollectedNode{ID: n.ID, ParentID: parentID, Title: n.Title, PageID: pageID})
			if pageID != "" {
				if _, ok := seenPage[pageID]; !ok {
					seenPage[pageID] = struct{}{}
					pb, perr := a.getBodyTimed(ctx, spec.BaseURL+"/api/page/"+pubID+"/"+pageID, maxPageBytes)
					if perr != nil {
						return perr
					}
					if err := zipWriteBytes(zw, "page-"+pageID+".json", pb); err != nil {
						return err
					}
				}
			}
			sleepCtx(ctx, crawlDelay)
			if n.HasChildNodes {
				if err := walk(n.ID, n.ID); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := walk(rootID, rootID); err != nil {
		_ = zw.Close()
		return err
	}
	tocJSON, _ := json.Marshal(collected)
	if err := zipWriteBytes(zw, "toc.json", tocJSON); err != nil {
		_ = zw.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		return sources.NewAdapterError(sources.ErrCodeNetwork, "finalize EUIPO archive", err)
	}
	return nil
}

// euipoPageID extracts the page id from a toc Url like
// "/2302857/2231846/trade-mark-guidelines/1-introduction" -> "2231846".
func euipoPageID(u *string) string {
	if u == nil {
		return ""
	}
	parts := strings.Split(strings.Trim(*u, "/"), "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func zipWriteBytes(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("zip create %s: %w", name, err)
	}
	_, err = w.Write(data)
	return err
}
