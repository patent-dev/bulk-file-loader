package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// heartbeatKey is the setting a running server refreshes every
// heartbeatInterval; a heartbeat older than serverStale means no server runs.
const heartbeatKey = "server_heartbeat"

var (
	heartbeatInterval = 10 * time.Second
	serverStale       = 30 * time.Second
	reloadInterval    = 30 * time.Second
)

// ServerHeartbeat identifies the running server.
type ServerHeartbeat struct {
	At        time.Time `json:"at"`
	StartedAt time.Time `json:"startedAt"`
	Owner     string    `json:"owner"`
	Version   string    `json:"version"`
}

// Owner identifies this process as a queue worker: host and process ID.
func Owner() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}

// RunServer starts what only the server does: the download workers, the
// heartbeat that tells CLI commands a server owns the queue, and reloading
// schedules changed by the CLI. Everything stops when the service context
// is cancelled.
func (s *Service) RunServer() {
	owner := Owner()
	s.goBackground(func(ctx context.Context) { s.Downloader.RunWorkers(ctx, owner, nil) })
	s.goBackground(func(ctx context.Context) {
		started := time.Now()
		beat := func() {
			hb, _ := json.Marshal(ServerHeartbeat{At: time.Now(), StartedAt: started, Owner: owner, Version: s.Version})
			if err := s.DB.SetSetting(heartbeatKey, string(hb)); err != nil {
				slog.Error("Failed to write the server heartbeat", "error", err)
			}
		}
		beat()
		heartbeat := time.NewTicker(heartbeatInterval)
		reload := time.NewTicker(reloadInterval)
		defer heartbeat.Stop()
		defer reload.Stop()
		for {
			select {
			case <-ctx.Done():
				// Tell CLI commands right away that no server owns the queue.
				// Only our own heartbeat: another server may have taken over.
				if hb := s.Server(); hb != nil && hb.Owner == owner {
					if err := s.DB.DeleteSetting(heartbeatKey); err != nil {
						slog.Error("Failed to clear the server heartbeat", "error", err)
					}
				}
				return
			case <-heartbeat.C:
				beat()
			case <-reload.C:
				if s.Scheduler != nil {
					s.Scheduler.Reload()
				}
			}
		}
	})
}

// Server returns the running server's heartbeat, or nil when no server has
// written one recently. Hosts sharing a database need synchronised clocks
// (NTP): the heartbeat's age is measured against this host's clock.
func (s *Service) Server() *ServerHeartbeat {
	raw, err := s.DB.GetSetting(heartbeatKey)
	if err != nil {
		return nil
	}
	var hb ServerHeartbeat
	if json.Unmarshal([]byte(raw), &hb) != nil || time.Since(hb.At) > serverStale {
		return nil
	}
	return &hb
}

// Transfer is a download in progress in any process, from the progress the
// downloader stores every few seconds.
type Transfer struct {
	FileID     string
	FileName   string
	Bytes      int64
	TotalBytes int64
	StartedAt  time.Time
}

// Transfers lists downloads in progress in any process.
func (s *Service) Transfers() ([]Transfer, error) {
	var rows []struct {
		FileID     string
		FileName   string
		Progress   int64
		TotalBytes int64
		StartedAt  *time.Time
	}
	err := s.DB.Table("download_entries").
		Select("download_entries.file_id, files.file_name, download_entries.progress, download_entries.total_bytes, download_entries.started_at").
		Joins("JOIN files ON files.id = download_entries.file_id").
		Where("download_entries.status = ?", "downloading").
		Order("download_entries.started_at").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Transfer, 0, len(rows))
	for _, r := range rows {
		t := Transfer{FileID: r.FileID, FileName: r.FileName, Bytes: r.Progress, TotalBytes: r.TotalBytes}
		if r.StartedAt != nil {
			t.StartedAt = *r.StartedAt
		}
		out = append(out, t)
	}
	return out, nil
}
