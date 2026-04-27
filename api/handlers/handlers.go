package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/auth"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/service"
)

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) AuthService() *auth.Service {
	return h.svc.Auth
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, generated.Error{Message: message})
}

func decodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20) // 1 MB limit
	return json.NewDecoder(r.Body).Decode(v)
}

func (h *Handler) GetAuthStatus(w http.ResponseWriter, r *http.Request) {
	authenticated := h.svc.Auth.CheckAuthentication(r)
	writeJSON(w, http.StatusOK, generated.AuthStatus{
		Configured:    h.svc.Auth.IsConfigured(),
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

	if err := h.svc.Auth.Setup(req.Passphrase); err != nil {
		if errors.Is(err, auth.ErrAlreadyConfigured) {
			writeError(w, http.StatusBadRequest, "Already configured")
			return
		}
		writeError(w, http.StatusInternalServerError, "Setup failed")
		return
	}

	_ = h.svc.Auth.Login(w, r, req.Passphrase)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req generated.LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.svc.Auth.Login(w, r, req.Passphrase); err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid passphrase")
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.svc.Auth.Logout(w, r)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) ListSources(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.ListSources()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list sources")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetSource(w http.ResponseWriter, r *http.Request, id string) {
	source, err := h.svc.GetSource(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Source not found")
		return
	}
	writeJSON(w, http.StatusOK, source)
}

func (h *Handler) UpdateSource(w http.ResponseWriter, r *http.Request, id string) {
	var req generated.UpdateSourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	source, err := h.svc.UpdateSource(r.Context(), id, req.Enabled, req.Credentials)
	if err != nil {
		slog.Error("Failed to update source", "id", id, "error", err)
		if errors.Is(err, service.ErrInvalidCredentials) {
			writeError(w, http.StatusBadRequest, "Invalid credentials")
		} else {
			writeError(w, http.StatusBadRequest, "Failed to update source")
		}
		return
	}
	writeJSON(w, http.StatusOK, source)
}

func (h *Handler) TestSourceCredentials(w http.ResponseWriter, r *http.Request, id string) {
	var req generated.TestCredentialsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.svc.TestCredentials(r.Context(), id, req.Credentials); err != nil {
		slog.Error("Credential test failed", "source", id, "error", err)
		writeError(w, http.StatusUnauthorized, "Credential validation failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) ListProducts(w http.ResponseWriter, r *http.Request, params generated.ListProductsParams) {
	result, err := h.svc.ListProducts(params.SourceId)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list products")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetProduct(w http.ResponseWriter, r *http.Request, id string) {
	result, err := h.svc.GetProduct(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Product not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) SyncProduct(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.svc.SyncProduct(r.Context(), id); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Product not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to sync product")
		}
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) ListFiles(w http.ResponseWriter, r *http.Request, params generated.ListFilesParams) {
	offset := 0
	limit := 50
	if params.Offset != nil {
		offset = *params.Offset
	}
	if params.Limit != nil {
		limit = *params.Limit
	}

	result, err := h.svc.ListFiles(params.SourceId, params.ProductId, params.Status, offset, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list files")
		return
	}
	writeJSON(w, http.StatusOK, generated.FileListResponse{
		Files: result.Files,
		Total: result.Total,
	})
}

func (h *Handler) GetFile(w http.ResponseWriter, r *http.Request, id string) {
	result, err := h.svc.GetFile(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "File not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.svc.DeleteFile(id); err != nil {
		slog.Error("Failed to delete file", "id", id, "error", err)
		if errors.Is(err, service.ErrNotFound) {
			writeError(w, http.StatusNotFound, "No downloaded file found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to delete file")
		}
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) DownloadFile(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := h.svc.GetFile(id); err != nil {
		writeError(w, http.StatusNotFound, "File not found")
		return
	}
	h.svc.DownloadFile(id)
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) CancelDownload(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.svc.CancelDownload(id); err != nil {
		writeError(w, http.StatusNotFound, "Download not found or not in progress")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) SkipFile(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.svc.SkipFile(id); err != nil {
		writeError(w, http.StatusNotFound, "File not found")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) UnskipFile(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.svc.UnskipFile(id); err != nil {
		writeError(w, http.StatusNotFound, "File not found")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) ResetFile(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.svc.ResetFile(id); err != nil {
		slog.Error("Failed to reset file", "id", id, "error", err)
		switch {
		case errors.Is(err, service.ErrConflict):
			writeError(w, http.StatusConflict, "Cannot reset file while download is in progress")
		case errors.Is(err, service.ErrNotFound):
			writeError(w, http.StatusNotFound, "No download history found")
		default:
			writeError(w, http.StatusInternalServerError, "Failed to reset file")
		}
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) ListDownloads(w http.ResponseWriter, r *http.Request, params generated.ListDownloadsParams) {
	offset := 0
	limit := 50
	if params.Offset != nil {
		offset = *params.Offset
	}
	if params.Limit != nil {
		limit = *params.Limit
	}

	var status *string
	if params.Status != nil {
		s := string(*params.Status)
		status = &s
	}

	result, err := h.svc.ListDownloads(status, offset, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list downloads")
		return
	}
	writeJSON(w, http.StatusOK, generated.DownloadListResponse{
		Downloads: result.Downloads,
		Total:     result.Total,
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
			payload := struct {
				Downloads     []downloader.DownloadProgress `json:"downloads"`
				StatusVersion uint64                        `json:"statusVersion"`
			}{
				Downloads:     h.svc.ActiveDownloads(),
				StatusVersion: h.svc.StatusVersion(),
			}
			data, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (h *Handler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.GetSchedule()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to get schedule")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) UpdateProductSchedule(w http.ResponseWriter, r *http.Request, productID string) {
	var req generated.UpdateScheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	schedule, err := h.svc.UpdateProductSchedule(productID, req)
	if err != nil {
		slog.Error("Failed to update product schedule", "productID", productID, "error", err)
		switch {
		case errors.Is(err, service.ErrNotFound):
			writeError(w, http.StatusNotFound, "Product not found")
		case errors.Is(err, service.ErrInvalidSchedule):
			writeError(w, http.StatusBadRequest, "Invalid schedule configuration")
		default:
			writeError(w, http.StatusInternalServerError, "Failed to update schedule")
		}
		return
	}
	writeJSON(w, http.StatusOK, schedule)
}

func (h *Handler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.ListWebhooks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list webhooks")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	var req generated.CreateWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	webhook, err := h.svc.CreateWebhook(req.Name, req.Url, req.Events)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create webhook")
		return
	}
	writeJSON(w, http.StatusCreated, webhook)
}

func (h *Handler) UpdateWebhook(w http.ResponseWriter, r *http.Request, id int) {
	var req generated.UpdateWebhookRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	webhook, err := h.svc.UpdateWebhook(id, req)
	if err != nil {
		slog.Error("Failed to update webhook", "id", id, "error", err)
		if errors.Is(err, service.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Webhook not found")
		} else {
			writeError(w, http.StatusInternalServerError, "Failed to update webhook")
		}
		return
	}
	writeJSON(w, http.StatusOK, webhook)
}

func (h *Handler) DeleteWebhook(w http.ResponseWriter, r *http.Request, id int) {
	if err := h.svc.DeleteWebhook(id); err != nil {
		writeError(w, http.StatusNotFound, "Webhook not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.HealthCheck())
}

func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.svc.GetStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to get stats")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
