package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/auth"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/scheduler"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

var startTime = time.Now()

type Handler struct {
	db         *database.DB
	auth       *auth.Service
	registry   *sources.Registry
	downloader *downloader.Downloader
	scheduler  *scheduler.Scheduler
	hooks      *hooks.Manager
}

func New(
	db *database.DB,
	authService *auth.Service,
	registry *sources.Registry,
	dl *downloader.Downloader,
	sched *scheduler.Scheduler,
	hooksManager *hooks.Manager,
) *Handler {
	return &Handler{
		db:         db,
		auth:       authService,
		registry:   registry,
		downloader: dl,
		scheduler:  sched,
		hooks:      hooksManager,
	}
}

// Helper functions
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, generated.Error{Message: message})
}

func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// Auth handlers

func (h *Handler) GetAuthStatus(w http.ResponseWriter, r *http.Request) {
	// Check if user is authenticated by validating cookie directly
	// (this endpoint doesn't go through auth middleware)
	authenticated := h.auth.CheckAuthentication(r)

	writeJSON(w, http.StatusOK, generated.AuthStatus{
		Configured:    h.auth.IsConfigured(),
		Authenticated: authenticated,
	})
}

func (h *Handler) SetupAuth(w http.ResponseWriter, r *http.Request) {
	var req generated.SetupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if len(req.Passphrase) < 8 {
		writeError(w, http.StatusBadRequest, "Passphrase must be at least 8 characters")
		return
	}

	if err := h.auth.Setup(req.Passphrase); err != nil {
		if err == auth.ErrAlreadyConfigured {
			writeError(w, http.StatusBadRequest, "Already configured")
			return
		}
		writeError(w, http.StatusInternalServerError, "Setup failed")
		return
	}

	// Auto-login after setup
	h.auth.Login(w, req.Passphrase)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req generated.LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.auth.Login(w, req.Passphrase); err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid passphrase")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.auth.Logout(w)
	w.WriteHeader(http.StatusOK)
}

// Source handlers

func (h *Handler) ListSources(w http.ResponseWriter, r *http.Request) {
	sourceInfos, err := h.registry.ListSources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list sources")
		return
	}

	result := make([]generated.Source, 0, len(sourceInfos))
	for _, si := range sourceInfos {
		source := generated.Source{
			Id:             si.ID,
			Name:           si.Name,
			Enabled:        si.Enabled,
			HasCredentials: si.HasCredentials,
			LastSyncAt:     si.LastSyncAt,
		}
		for _, cf := range si.CredentialFields {
			helpText := cf.HelpText
			source.CredentialFields = append(source.CredentialFields, generated.CredentialField{
				Key:      cf.Key,
				Label:    cf.Label,
				Type:     generated.CredentialFieldType(cf.Type),
				Required: cf.Required,
				HelpText: &helpText,
			})
		}
		result = append(result, source)
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetSource(w http.ResponseWriter, r *http.Request, id string) {
	si, err := h.registry.GetSource(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Source not found")
		return
	}

	source := generated.Source{
		Id:             si.ID,
		Name:           si.Name,
		Enabled:        si.Enabled,
		HasCredentials: si.HasCredentials,
		LastSyncAt:     si.LastSyncAt,
	}
	for _, cf := range si.CredentialFields {
		helpText := cf.HelpText
		source.CredentialFields = append(source.CredentialFields, generated.CredentialField{
			Key:      cf.Key,
			Label:    cf.Label,
			Type:     generated.CredentialFieldType(cf.Type),
			Required: cf.Required,
			HelpText: &helpText,
		})
	}

	writeJSON(w, http.StatusOK, source)
}

func (h *Handler) UpdateSource(w http.ResponseWriter, r *http.Request, id string) {
	var req generated.UpdateSourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	enabled := false
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	var creds map[string]string
	if req.Credentials != nil {
		creds = *req.Credentials
	}

	// Validate credentials before enabling with new credentials
	if enabled && creds != nil {
		adapter, ok := h.registry.Get(id)
		if ok {
			// Temporarily set credentials to validate
			adapter.SetCredentials(creds)
			if err := adapter.ValidateCredentials(r.Context()); err != nil {
				writeError(w, http.StatusBadRequest, "Invalid credentials: "+err.Error())
				return
			}
		}
	}

	if err := h.registry.UpdateSource(id, enabled, creds, h.auth); err != nil {
		slog.Error("Failed to update source", "source", id, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// When enabling, sync products synchronously so they appear immediately
	// Files are synced in background since that takes longer
	if enabled {
		h.syncProductsOnly(id)
		go h.syncProductFiles(id)
	}

	h.GetSource(w, r, id)
}

// syncProductsOnly fetches and saves products synchronously (no files)
func (h *Handler) syncProductsOnly(sourceID string) {
	ctx := context.Background()
	slog.Info("Syncing products", "source", sourceID)

	adapter, ok := h.registry.Get(sourceID)
	if !ok {
		slog.Error("Adapter not found", "source", sourceID)
		return
	}

	products, err := adapter.FetchProducts(ctx)
	if err != nil {
		slog.Error("Failed to fetch products", "source", sourceID, "error", err)
		return
	}

	slog.Info("Found products", "source", sourceID, "count", len(products))
	for _, p := range products {
		productID := fmt.Sprintf("%s:%s", sourceID, p.ExternalID)
		product := database.Product{
			ID:               productID,
			SourceID:         sourceID,
			ExternalID:       p.ExternalID,
			Name:             p.Name,
			Description:      p.Description,
			CheckWindowStart: p.CheckSchedule,
		}
		if err := h.db.Save(&product).Error; err != nil {
			slog.Error("Failed to save product", "productID", productID, "error", err)
		}
	}
}

// syncProductFiles syncs deliveries and files for all products of a source (background)
func (h *Handler) syncProductFiles(sourceID string) {
	ctx := context.Background()
	slog.Info("Syncing files", "source", sourceID)

	adapter, ok := h.registry.Get(sourceID)
	if !ok {
		return
	}

	var products []database.Product
	if err := h.db.Where("source_id = ?", sourceID).Find(&products).Error; err != nil {
		slog.Error("Failed to get products", "source", sourceID, "error", err)
		return
	}

	for _, p := range products {
		h.syncProductDeliveriesAndFiles(ctx, adapter, sourceID, p.ID, p.ExternalID)
	}
	slog.Info("File sync completed", "source", sourceID)
}

func (h *Handler) syncProductDeliveriesAndFiles(ctx context.Context, adapter sources.Adapter, sourceID, productID, externalProductID string) {
	deliveries, err := adapter.FetchDeliveries(ctx, externalProductID)
	if err != nil {
		slog.Error("Failed to fetch deliveries", "product", productID, "error", err)
		return
	}

	totalFiles := 0
	for _, d := range deliveries {
		deliveryID := fmt.Sprintf("%s:%s", productID, d.ExternalID)
		delivery := database.Delivery{
			ID:          deliveryID,
			ProductID:   productID,
			ExternalID:  d.ExternalID,
			Name:        d.Name,
			PublishedAt: &d.PublishedAt,
			ExpiresAt:   d.ExpiresAt,
		}
		if err := h.db.Save(&delivery).Error; err != nil {
			slog.Error("Failed to save delivery", "deliveryID", deliveryID, "error", err)
			continue
		}

		files, err := adapter.FetchFiles(ctx, externalProductID, d.ExternalID)
		if err != nil {
			slog.Error("Failed to fetch files", "deliveryID", deliveryID, "error", err)
			continue
		}

		for _, f := range files {
			fileID := fmt.Sprintf("%s:%s", deliveryID, f.ExternalID)
			file := database.File{
				ID:                fileID,
				DeliveryID:        deliveryID,
				ProductID:         productID,
				SourceID:          sourceID,
				ExternalID:        f.ExternalID,
				FileName:          f.FileName,
				FileSize:          f.FileSize,
				ExpectedChecksum:  f.Checksum,
				ChecksumAlgorithm: f.ChecksumAlgorithm,
				DownloadURI:       f.DownloadURI,
				ReleasedAt:        &f.ReleasedAt,
			}
			if err := h.db.Save(&file).Error; err != nil {
				slog.Error("Failed to save file", "fileID", fileID, "error", err)
				continue
			}
			totalFiles++
		}
	}
	slog.Debug("Synced files", "product", productID, "count", totalFiles)
}

func (h *Handler) downloadPendingFiles(productID string) {
	var files []database.File
	h.db.Where("product_id = ? AND skipped = ?", productID, false).Find(&files)

	for _, file := range files {
		var entry database.DownloadEntry
		err := h.db.Where("file_id = ? AND status = ?", file.ID, database.DownloadStatusCompleted).First(&entry).Error
		if err == nil {
			continue
		}

		go func(f database.File) {
			if err := h.downloader.Download(context.Background(), f.ID); err != nil {
				slog.Error("Auto-download failed", "file", f.FileName, "error", err)
			}
		}(file)
	}
}

func (h *Handler) TestSourceCredentials(w http.ResponseWriter, r *http.Request, id string) {
	var req generated.TestCredentialsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.registry.TestCredentials(r.Context(), id, req.Credentials); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

// Product handlers

func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request, params generated.ListProductsParams) {
	var products []database.Product
	query := h.db.DB

	if params.SourceId != nil {
		query = query.Where("source_id = ?", *params.SourceId)
	}

	if err := query.Order("name ASC").Find(&products).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list products")
		return
	}

	result := make([]generated.Product, 0, len(products))
	for _, p := range products {
		product := convertProduct(p)

		// Add file counts
		var totalFiles, downloadedFiles, failedFiles int64
		h.db.Model(&database.File{}).Where("product_id = ?", p.ID).Count(&totalFiles)
		h.db.Model(&database.DownloadEntry{}).
			Joins("JOIN files ON files.id = download_entries.file_id").
			Where("files.product_id = ? AND download_entries.status = ?", p.ID, "completed").
			Distinct("file_id").Count(&downloadedFiles)
		// Count files where the most recent download entry is "failed"
		h.db.Raw(`
			SELECT COUNT(DISTINCT de.file_id) FROM download_entries de
			JOIN files f ON f.id = de.file_id
			WHERE f.product_id = ?
			AND de.status = 'failed'
			AND de.id = (SELECT MAX(de2.id) FROM download_entries de2 WHERE de2.file_id = de.file_id)
		`, p.ID).Scan(&failedFiles)

		tf := int(totalFiles)
		df := int(downloadedFiles)
		ff := int(failedFiles)
		product.TotalFiles = &tf
		product.DownloadedFiles = &df
		product.FailedFiles = &ff

		result = append(result, product)
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetProduct(w http.ResponseWriter, r *http.Request, id string) {
	var product database.Product
	if err := h.db.Preload("Deliveries.Files").First(&product, "id = ?", id).Error; err != nil {
		writeError(w, http.StatusNotFound, "Product not found")
		return
	}

	p := convertProduct(product)
	result := generated.ProductWithDeliveries{
		Id:               p.Id,
		SourceId:         p.SourceId,
		Name:             p.Name,
		AutoDownload:     p.AutoDownload,
		ExternalId:       p.ExternalId,
		Description:      p.Description,
		CheckWindowStart: p.CheckWindowStart,
		LastCheckedAt:    p.LastCheckedAt,
		TotalFiles:       p.TotalFiles,
		DownloadedFiles:  p.DownloadedFiles,
		FailedFiles:      p.FailedFiles,
	}

	deliveries := make([]generated.Delivery, 0, len(product.Deliveries))
	for _, d := range product.Deliveries {
		deliveries = append(deliveries, convertDelivery(d))
	}
	result.Deliveries = &deliveries

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) SyncProduct(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.scheduler.SyncNow(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "Product not found")
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// File handlers

func (h *Handler) ListFiles(w http.ResponseWriter, r *http.Request, params generated.ListFilesParams) {
	var files []database.File
	var total int64

	query := h.db.DB.Model(&database.File{})

	if params.SourceId != nil {
		query = query.Where("source_id = ?", *params.SourceId)
	}
	if params.ProductId != nil {
		query = query.Where("product_id = ?", *params.ProductId)
	}

	query.Count(&total)

	offset := 0
	limit := 50
	if params.Offset != nil {
		offset = *params.Offset
	}
	if params.Limit != nil {
		limit = *params.Limit
	}

	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&files).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list files")
		return
	}

	result := make([]generated.File, 0, len(files))
	for _, f := range files {
		result = append(result, convertFile(f, h.db))
	}

	// Filter by status if provided (done after conversion since status is derived)
	if params.Status != nil {
		filtered := make([]generated.File, 0)
		for _, f := range result {
			if string(f.Status) == string(*params.Status) {
				filtered = append(filtered, f)
			}
		}
		result = filtered
	}

	writeJSON(w, http.StatusOK, generated.FileListResponse{
		Files: result,
		Total: int(total),
	})
}

func (h *Handler) GetFile(w http.ResponseWriter, r *http.Request, id string) {
	var file database.File
	if err := h.db.Preload("DownloadEntries").First(&file, "id = ?", id).Error; err != nil {
		writeError(w, http.StatusNotFound, "File not found")
		return
	}

	f := convertFile(file, h.db)
	result := generated.FileWithHistory{
		Id:               f.Id,
		FileName:         f.FileName,
		FileSize:         f.FileSize,
		Status:           generated.FileWithHistoryStatus(f.Status),
		ErrorMessage:     f.ErrorMessage,
		DeliveryId:       f.DeliveryId,
		ProductId:        f.ProductId,
		SourceId:         f.SourceId,
		ExpectedChecksum: f.ExpectedChecksum,
		ReleasedAt:       f.ReleasedAt,
		Skipped:          f.Skipped,
	}

	history := make([]generated.DownloadEntry, 0, len(file.DownloadEntries))
	for _, e := range file.DownloadEntries {
		history = append(history, convertDownloadEntry(e))
	}
	result.DownloadHistory = &history

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request, id string) {
	// Find the most recent completed download entry
	var entry database.DownloadEntry
	if err := h.db.Where("file_id = ? AND status = ?", id, "completed").Order("completed_at DESC").First(&entry).Error; err != nil {
		writeError(w, http.StatusNotFound, "No downloaded file found")
		return
	}

	// Delete the file from disk
	if entry.LocalPath != "" {
		if err := os.Remove(entry.LocalPath); err != nil && !os.IsNotExist(err) {
			slog.Error("Failed to delete file", "path", entry.LocalPath, "error", err)
			writeError(w, http.StatusInternalServerError, "Failed to delete file")
			return
		}
	}

	// Update download entry status to deleted
	h.db.Model(&entry).Update("status", "deleted")

	slog.Info("File deleted", "fileID", id, "path", entry.LocalPath)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) DownloadFile(w http.ResponseWriter, r *http.Request, id string) {
	go func() {
		ctx := context.Background()
		h.downloader.Download(ctx, id)
	}()

	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) CancelDownload(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.downloader.Cancel(id); err != nil {
		writeError(w, http.StatusNotFound, "Download not found or not in progress")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) SkipFile(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.db.Model(&database.File{}).Where("id = ?", id).Update("skipped", true).Error; err != nil {
		writeError(w, http.StatusNotFound, "File not found")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UnskipFile(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.db.Model(&database.File{}).Where("id = ?", id).Update("skipped", false).Error; err != nil {
		writeError(w, http.StatusNotFound, "File not found")
		return
	}

	w.WriteHeader(http.StatusOK)
}

// Download handlers

func (h *Handler) ListDownloads(w http.ResponseWriter, r *http.Request, params generated.ListDownloadsParams) {
	var entries []database.DownloadEntry
	var total int64

	query := h.db.DB.Model(&database.DownloadEntry{})

	if params.Status != nil {
		query = query.Where("status = ?", *params.Status)
	}

	query.Count(&total)

	offset := 0
	limit := 50
	if params.Offset != nil {
		offset = *params.Offset
	}
	if params.Limit != nil {
		limit = *params.Limit
	}

	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&entries).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list downloads")
		return
	}

	result := make([]generated.DownloadEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, convertDownloadEntry(e))
	}

	writeJSON(w, http.StatusOK, generated.DownloadListResponse{
		Downloads: result,
		Total:     int(total),
	})
}

func (h *Handler) StreamActiveDownloads(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx/traefik buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			downloads := h.downloader.ActiveDownloads()
			data, _ := json.Marshal(downloads)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// Schedule handlers

func (h *Handler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	var products []database.Product
	if err := h.db.Find(&products).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to get schedule")
		return
	}

	result := make([]generated.ProductSchedule, 0, len(products))
	for _, p := range products {
		schedule := generated.ProductSchedule{
			ProductId:    p.ID,
			ProductName:  p.Name,
			AutoDownload: p.AutoDownload,
		}
		if p.CheckWindowStart != "" {
			schedule.CheckWindowStart = &p.CheckWindowStart
		}
		if p.CheckWindowEnd != "" {
			schedule.CheckWindowEnd = &p.CheckWindowEnd
		}
		if nextRun := h.scheduler.GetNextRun(p.ID); nextRun != nil {
			schedule.NextRun = nextRun
		}
		result = append(result, schedule)
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) UpdateProductSchedule(w http.ResponseWriter, r *http.Request, productID string) {
	var req generated.UpdateScheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var product database.Product
	if err := h.db.First(&product, "id = ?", productID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Product not found")
		return
	}

	wasAutoDownload := product.AutoDownload

	if req.AutoDownload != nil {
		product.AutoDownload = *req.AutoDownload
	}
	if req.CheckWindowStart != nil {
		product.CheckWindowStart = *req.CheckWindowStart
	}
	if req.CheckWindowEnd != nil {
		product.CheckWindowEnd = *req.CheckWindowEnd
	}

	// Validate schedule before saving
	if err := h.scheduler.ScheduleProduct(&product); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid schedule: "+err.Error())
		return
	}

	if err := h.db.Save(&product).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update schedule")
		return
	}

	// If auto-download was just enabled, trigger immediate download of pending files
	if product.AutoDownload && !wasAutoDownload {
		go h.downloadPendingFiles(product.ID)
	}

	schedule := generated.ProductSchedule{
		ProductId:    product.ID,
		ProductName:  product.Name,
		AutoDownload: product.AutoDownload,
	}
	if product.CheckWindowStart != "" {
		schedule.CheckWindowStart = &product.CheckWindowStart
	}
	if nextRun := h.scheduler.GetNextRun(product.ID); nextRun != nil {
		schedule.NextRun = nextRun
	}

	writeJSON(w, http.StatusOK, schedule)
}

// Webhook handlers

func (h *Handler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	webhooks, err := h.hooks.ListWebhooks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list webhooks")
		return
	}

	result := make([]generated.Webhook, 0, len(webhooks))
	for _, wh := range webhooks {
		result = append(result, convertWebhook(wh))
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	var req generated.CreateWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	webhook, err := h.hooks.CreateWebhook(req.Name, req.Url, req.Events)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create webhook")
		return
	}

	writeJSON(w, http.StatusCreated, convertWebhook(*webhook))
}

func (h *Handler) UpdateWebhook(w http.ResponseWriter, r *http.Request, id int) {
	var req generated.UpdateWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	webhook, err := h.hooks.GetWebhook(uint(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Webhook not found")
		return
	}

	name := webhook.Name
	url := webhook.URL
	events := hooks.ParseEvents(webhook.Events)
	enabled := webhook.Enabled

	if req.Name != nil {
		name = *req.Name
	}
	if req.Url != nil {
		url = *req.Url
	}
	if req.Events != nil {
		events = *req.Events
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if err := h.hooks.UpdateWebhook(uint(id), name, url, events, enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update webhook")
		return
	}

	updated, _ := h.hooks.GetWebhook(uint(id))
	writeJSON(w, http.StatusOK, convertWebhook(*updated))
}

func (h *Handler) DeleteWebhook(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.hooks.DeleteWebhook(uint(id)); err != nil {
		writeError(w, http.StatusNotFound, "Webhook not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// System handlers

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(startTime).String()
	version := "0.1.0"

	writeJSON(w, http.StatusOK, generated.HealthResponse{
		Status:  "healthy",
		Uptime:  &uptime,
		Version: &version,
	})
}

func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	var totalFiles, downloadedFiles, pendingFiles int64
	var enabledSources int64

	h.db.Model(&database.File{}).Count(&totalFiles)
	h.db.Model(&database.Source{}).Where("enabled = ?", true).Count(&enabledSources)

	// Count downloaded files (have at least one completed download entry)
	h.db.Model(&database.DownloadEntry{}).Where("status = ?", "completed").
		Distinct("file_id").Count(&downloadedFiles)

	// Count pending files: from auto-download products, not skipped, truly available (no download attempts)
	h.db.Model(&database.File{}).
		Joins("JOIN products ON products.id = files.product_id").
		Where("products.auto_download = ?", true).
		Where("files.skipped = ?", false).
		Where("files.id NOT IN (SELECT DISTINCT file_id FROM download_entries)").
		Count(&pendingFiles)

	activeDownloads := len(h.downloader.ActiveDownloads())

	tf := int(totalFiles)
	df := int(downloadedFiles)
	pf := int(pendingFiles)
	ad := activeDownloads
	es := int(enabledSources)

	writeJSON(w, http.StatusOK, generated.StatsResponse{
		TotalFiles:      &tf,
		DownloadedFiles: &df,
		PendingFiles:    &pf,
		ActiveDownloads: &ad,
		EnabledSources:  &es,
	})
}

// Conversion helpers

func convertProduct(p database.Product) generated.Product {
	result := generated.Product{
		Id:           p.ID,
		SourceId:     p.SourceID,
		Name:         p.Name,
		AutoDownload: p.AutoDownload,
	}
	if p.ExternalID != "" {
		result.ExternalId = &p.ExternalID
	}
	if p.Description != "" {
		result.Description = &p.Description
	}
	if p.CheckWindowStart != "" {
		result.CheckWindowStart = &p.CheckWindowStart
	}
	if p.LastCheckedAt != nil {
		result.LastCheckedAt = p.LastCheckedAt
	}
	return result
}

func convertDelivery(d database.Delivery) generated.Delivery {
	result := generated.Delivery{
		Id:        d.ID,
		ProductId: d.ProductID,
		Name:      d.Name,
	}
	if d.ExternalID != "" {
		result.ExternalId = &d.ExternalID
	}
	if d.PublishedAt != nil {
		result.PublishedAt = d.PublishedAt
	}
	if d.ExpiresAt != nil {
		result.ExpiresAt = d.ExpiresAt
	}
	return result
}

func convertFile(f database.File, db *database.DB) generated.File {
	status, errorMsg := deriveFileStatusAndError(f, db)
	result := generated.File{
		Id:       f.ID,
		FileName: f.FileName,
		FileSize: &f.FileSize,
		Status:   generated.FileStatus(status),
	}
	if errorMsg != "" {
		result.ErrorMessage = &errorMsg
	}
	if f.DeliveryID != "" {
		result.DeliveryId = &f.DeliveryID
	}
	if f.ProductID != "" {
		result.ProductId = &f.ProductID
	}
	if f.SourceID != "" {
		result.SourceId = &f.SourceID
	}
	if f.ExpectedChecksum != "" {
		result.ExpectedChecksum = &f.ExpectedChecksum
	}
	if f.ReleasedAt != nil {
		result.ReleasedAt = f.ReleasedAt
	}
	result.Skipped = &f.Skipped
	return result
}

func deriveFileStatusAndError(f database.File, db *database.DB) (string, string) {
	// Check latest download entry
	var entry database.DownloadEntry
	err := db.Where("file_id = ?", f.ID).Order("created_at DESC").First(&entry).Error
	if err == nil {
		switch entry.Status {
		case database.DownloadStatusDownloading:
			return "downloading", ""
		case database.DownloadStatusCompleted:
			// Check if file exists on disk
			if entry.LocalPath != "" {
				if _, err := os.Stat(entry.LocalPath); err == nil {
					return "downloaded", ""
				}
			}
			return "deleted", ""
		case database.DownloadStatusFailed:
			return "failed", entry.ErrorMessage
		case database.DownloadStatusCancelled:
			return "cancelled", ""
		}
	}

	if f.Skipped {
		return "skipped", ""
	}

	return "available", ""
}

func convertDownloadEntry(e database.DownloadEntry) generated.DownloadEntry {
	result := generated.DownloadEntry{
		Id:     int(e.ID),
		FileId: e.FileID,
		Status: generated.DownloadEntryStatus(e.Status),
	}
	if e.Progress > 0 {
		result.Progress = &e.Progress
	}
	if e.TotalBytes > 0 {
		result.TotalBytes = &e.TotalBytes
	}
	if e.LocalPath != "" {
		result.LocalPath = &e.LocalPath
	}
	if e.LocalChecksum != "" {
		result.LocalChecksum = &e.LocalChecksum
	}
	if e.ErrorMessage != "" {
		result.ErrorMessage = &e.ErrorMessage
	}
	if e.StartedAt != nil {
		result.StartedAt = e.StartedAt
	}
	if e.CompletedAt != nil {
		result.CompletedAt = e.CompletedAt
	}
	return result
}

func convertWebhook(wh database.Webhook) generated.Webhook {
	return generated.Webhook{
		Id:        int(wh.ID),
		Name:      wh.Name,
		Url:       wh.URL,
		Events:    hooks.ParseEvents(wh.Events),
		Enabled:   wh.Enabled,
		CreatedAt: &wh.CreatedAt,
	}
}
